package main

// The bridge: agents queue scripts, the Figma plugin pulls and runs them.
//
//   agent   POST /run?wait=N&file=F  script body     -> outcome (or {id} when wait=0)
//   agent   GET  /result?id=&wait=N                  -> outcome | {pending: true}
//   plugin  GET  /pull?plugin=&file=&key=&page=      -> {job: {id, code} | null}
//   plugin  GET  /pull?plugin=...&busy=<id>          -> heartbeat while a script runs
//   plugin  POST /result?id=<id>  {ok, result, ...}  -> {ok: true}
//   any     GET  /health                             -> liveness + the open files
//   window  GET  /  and  /status                     -> the status UI
//
// Every Figma file that runs the plugin is a separate plugin instance, and
// each script runs in exactly one of them. A script names its file with
// ?file=; one that doesn't goes to the only open file, and is refused when
// several are open, so it never lands in a guessed file.
//
// Scripts are eval'ed inside the user's Figma session, so the server binds to
// 127.0.0.1 only and refuses anything a web page could send: a foreign Host
// header (DNS rebinding), browser requests on the agent endpoints, and plugin
// requests without the token baked into the extracted plugin.

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed ui.html
var uiHTML []byte

const (
	maxScriptBytes = 4 << 20
	maxResultBytes = 64 << 20
	maxWait        = 600 * time.Second
	keepJobs       = 100
	// The plugin polls every 500ms, and heartbeats at the same pace while busy.
	pluginTimeout = 3 * time.Second
	// A running script whose plugin stops heartbeating is declared lost, and
	// so is a plugin that has not polled for this long.
	runTimeout = 15 * time.Second
	// After a start, every open plugin has polled within this time, so the
	// bridge knows all open files before it picks one for a script.
	settle = time.Second
)

// plugin is the Figma plugin running in one open file.
type plugin struct {
	ID    string // random, chosen by the plugin each time it starts
	File  string // the file's name
	Key   string // the file key in its URL, when Figma reveals it
	Page  string // the page open in that file
	First time.Time
	Seen  time.Time // last poll or heartbeat
}

// fileView is how agents and the window see an open file.
type fileView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Key  string `json:"key,omitempty"`
	Page string `json:"page"`
}

type job struct {
	ID      string
	Code    string // dropped once the plugin has pulled it
	Summary string
	Plugin  string // id of the plugin it must run in; "" = the first file to connect
	File    string // that plugin's file name
	State   string // queued | running | done | expired | refused
	OK      bool
	Result  any
	Error   string
	Stack   string
	Queued  time.Time
	Started time.Time
	Beat    time.Time // last plugin heartbeat while running
	Ended   time.Time
	done    chan struct{}
}

type bridge struct {
	port      int
	token     string
	pngDir    string
	pluginDir string
	skillPath string
	maxResult int64
	started   time.Time

	mu      sync.Mutex
	queue   []*job
	jobs    map[string]*job
	order   []*job // oldest first
	plugins map[string]*plugin
}

func newBridge(port int, token, pngDir string) *bridge {
	b := &bridge{
		port: port, token: token, pngDir: pngDir, maxResult: maxResultBytes, started: time.Now(),
		jobs: map[string]*job{}, plugins: map[string]*plugin{},
	}
	go func() {
		for now := range time.Tick(time.Second) {
			b.reap(now)
		}
	}()
	return b
}

func (b *bridge) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /run", agentOnly(b.handleRun))
	mux.HandleFunc("GET /result", agentOnly(b.handleGetResult))
	mux.HandleFunc("GET /pull", b.pluginOnly(b.handlePull))
	mux.HandleFunc("POST /result", b.pluginOnly(b.handlePostResult))
	mux.HandleFunc("OPTIONS /", cors(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("GET /health", b.handleHealth)
	mux.HandleFunc("GET /status", b.handleStatus)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(uiHTML)
	})
	return b.localOnly(mux)
}

// localOnly rejects requests addressed to any host but this loopback port,
// which defeats DNS-rebinding pages that resolve their own name to 127.0.0.1.
func (b *bridge) localOnly(next http.Handler) http.Handler {
	allowed := map[string]bool{
		fmt.Sprintf("127.0.0.1:%d", b.port): true,
		fmt.Sprintf("localhost:%d", b.port): true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.Host] {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden host"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// agentOnly refuses browser requests: browsers attach Origin to cross-origin
// and POST requests and Sec-Fetch-Site to all of them; curl sends neither.
func agentOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "browser requests are not accepted on agent endpoints"})
			return
		}
		h(w, r)
	}
}

// pluginOnly admits the Figma plugin, identified by the token that
// extractPlugin wrote into its UI. Its iframe has a null origin, hence CORS.
func (b *bridge) pluginOnly(h http.HandlerFunc) http.HandlerFunc {
	return cors(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-Marionette-Token")
		if subtle.ConstantTimeCompare([]byte(got), []byte(b.token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unknown plugin — re-import it from the Marionette window (Setup → Show in Finder)"})
			return
		}
		h(w, r)
	})
}

func cors(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Marionette-Token")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Max-Age", "600")
		h(w, r)
	}
}

func (b *bridge) handleRun(w http.ResponseWriter, r *http.Request) {
	code, err := readScript(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	wait, err := parseWait(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	// Just after a start, let every open plugin poll before picking a file.
	if d := time.Until(b.started.Add(settle)); d > 0 {
		time.Sleep(d)
	}
	j, queued, err := b.enqueue(code, r.URL.Query().Get("file"))
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "state": "choose_file", "error": err.Error(), "files": b.openFiles()})
		return
	}
	if wait == 0 {
		body := map[string]any{"id": j.ID, "queued": queued}
		if j.Plugin != "" {
			body["file"] = map[string]string{"id": j.Plugin, "name": j.File}
		}
		writeJSON(w, http.StatusOK, body)
		return
	}
	b.await(w, r, j, wait, true)
}

func (b *bridge) handleGetResult(w http.ResponseWriter, r *http.Request) {
	wait, err := parseWait(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	b.mu.Lock()
	j := b.jobs[r.URL.Query().Get("id")]
	b.mu.Unlock()
	if j == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown job id"})
		return
	}
	b.await(w, r, j, wait, false)
}

// await answers with the job's outcome once it ends, or after wait. Only the
// submitter (owner) may discard a still-queued job when it gives up; a mere
// poller gets {pending: true} instead.
func (b *bridge) await(w http.ResponseWriter, r *http.Request, j *job, wait time.Duration, owner bool) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-j.done:
		b.writeOutcome(w, j)
		return
	case <-r.Context().Done():
		if owner {
			// The agent gave up; don't let its script run later by surprise.
			b.expireIfQueued(j, "the agent disconnected before the Figma plugin picked this script up")
		}
		return
	case <-timer.C:
	}
	if owner {
		b.expireIfQueued(j, fmt.Sprintf("timeout after %s — the Figma plugin never picked this script up. Is Marionette running in Figma (Plugins → Development → Marionette)?", wait))
	}
	select {
	case <-j.done:
		b.writeOutcome(w, j)
		return
	default:
	}
	b.mu.Lock()
	state := j.State
	b.mu.Unlock()
	if !owner {
		writeJSON(w, http.StatusOK, map[string]any{"pending": true, "id": j.ID, "state": state})
		return
	}
	writeJSON(w, http.StatusGatewayTimeout, map[string]any{
		"ok": false, "id": j.ID, "state": state,
		"error": fmt.Sprintf("timeout after %s — the script is still running in Figma; poll GET /result?id=%s&wait=60", wait, j.ID),
	})
}

func (b *bridge) writeOutcome(w http.ResponseWriter, j *job) {
	b.mu.Lock()
	var code int
	var body map[string]any
	switch {
	case j.State == "expired":
		code, body = http.StatusGatewayTimeout, map[string]any{"ok": false, "id": j.ID, "state": "expired", "error": j.Error}
	case j.State == "refused":
		code, body = http.StatusConflict, map[string]any{"ok": false, "id": j.ID, "state": "choose_file", "error": j.Error, "files": b.openFilesLocked(time.Now())}
	case j.OK:
		code, body = http.StatusOK, map[string]any{"ok": true, "id": j.ID, "result": j.Result, "ms": j.Ended.Sub(j.Started).Milliseconds()}
	default:
		code, body = http.StatusUnprocessableEntity, map[string]any{"ok": false, "id": j.ID, "error": j.Error, "stack": j.Stack}
	}
	if j.State == "done" {
		body["file"] = map[string]string{"id": j.Plugin, "name": j.File}
	}
	b.mu.Unlock()
	writeJSON(w, code, body)
}

// enqueue queues a script for the file named by want (see pickLocked).
func (b *bridge) enqueue(code, want string) (*job, int, error) {
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	p, err := b.pickLocked(want, now)
	if err != nil {
		return nil, 0, err
	}
	j := &job{
		ID:      newID(),
		Code:    code,
		Summary: summarize(code),
		State:   "queued",
		Queued:  now,
		done:    make(chan struct{}),
	}
	if p != nil {
		j.Plugin, j.File = p.ID, p.File
	}
	b.queue = append(b.queue, j)
	b.jobs[j.ID] = j
	b.order = append(b.order, j)
	b.prune()
	return j, len(b.queue), nil
}

// pickLocked chooses the plugin a new script runs in. want names a file by
// plugin id, file key or file name; without it the script goes to the only
// open file, or to whichever connects first when none is open. It never
// guesses between several. Caller holds b.mu.
func (b *bridge) pickLocked(want string, now time.Time) (*plugin, error) {
	open := b.openLocked(now)
	match := open
	if want != "" {
		match = nil
		for _, p := range open {
			if want == p.ID || want == p.File || (p.Key != "" && want == p.Key) {
				match = append(match, p)
			}
		}
	}
	// Plugins sharing a file key are one file — the plugin was just reopened,
	// or the file is open twice — so the newest stands for it.
	if len(match) > 1 && !slices.ContainsFunc(match, func(p *plugin) bool { return p.Key == "" || p.Key != match[0].Key }) {
		match = match[len(match)-1:]
	}
	switch {
	case len(match) == 1:
		return match[0], nil
	case want == "" && len(open) == 0:
		return nil, nil
	case want == "":
		return nil, fmt.Errorf("%d Figma files have Marionette open, so the script was not run — ask the user which file to change, then pass it as /run?file=<id>", len(open))
	case len(match) == 0:
		return nil, fmt.Errorf("no Figma file with Marionette open matches %q, so the script was not run — pick one of the open files, or ask the user to run Marionette in that file", want)
	default:
		return nil, fmt.Errorf("%d open Figma files match %q, so the script was not run — pass the id of one of them", len(match), want)
	}
}

// openLocked lists the plugins that polled recently, in the order they
// connected. Caller holds b.mu.
func (b *bridge) openLocked(now time.Time) []*plugin {
	var open []*plugin
	for _, p := range b.plugins {
		if now.Sub(p.Seen) < pluginTimeout {
			open = append(open, p)
		}
	}
	slices.SortFunc(open, func(x, y *plugin) int { return x.First.Compare(y.First) })
	return open
}

func (b *bridge) openFiles() []fileView {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.openFilesLocked(time.Now())
}

// openFilesLocked is openLocked as agents see it: one entry per file, the
// newest plugin standing for a file it shares with others. Caller holds b.mu.
func (b *bridge) openFilesLocked(now time.Time) []fileView {
	files := []fileView{}
	byKey := map[string]int{}
	for _, p := range b.openLocked(now) {
		f := fileView{ID: p.ID, Name: p.File, Key: p.Key, Page: p.Page}
		if i, ok := byKey[p.Key]; ok {
			files[i] = f
			continue
		}
		if p.Key != "" {
			byKey[p.Key] = len(files)
		}
		files = append(files, f)
	}
	return files
}

// prune drops the oldest finished jobs beyond keepJobs. Caller holds b.mu.
func (b *bridge) prune() {
	for len(b.order) > keepJobs {
		i := 0
		for i < len(b.order) && (b.order[i].State == "queued" || b.order[i].State == "running") {
			i++
		}
		if i == len(b.order) {
			return
		}
		delete(b.jobs, b.order[i].ID)
		b.order = append(b.order[:i], b.order[i+1:]...)
	}
}

func (b *bridge) expireIfQueued(j *job, reason string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dropLocked(j, "expired", reason)
}

// dropLocked ends a still-queued job without running it. Caller holds b.mu.
func (b *bridge) dropLocked(j *job, state, reason string) {
	if j.State != "queued" {
		return
	}
	for i, q := range b.queue {
		if q == j {
			b.queue = append(b.queue[:i], b.queue[i+1:]...)
			break
		}
	}
	j.State = state
	j.Error = reason
	j.Ended = time.Now()
	close(j.done)
}

// finishLocked records a running job's outcome exactly once and reports
// whether it did. Caller holds b.mu.
func (b *bridge) finishLocked(j *job, ok bool, result any, errMsg, stack string) bool {
	if j.State != "running" {
		return false
	}
	j.State = "done"
	j.OK = ok
	j.Result = result
	j.Error = errMsg
	j.Stack = stack
	j.Ended = time.Now()
	close(j.done)
	return true
}

// reap fails running jobs whose plugin went quiet: its panel was closed, the
// script called figma.closePlugin(), or the result never made it back. It
// also drops scripts waiting for a file whose plugin is gone, and forgets
// that plugin.
func (b *bridge) reap(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	gone := func(id string) bool {
		p := b.plugins[id]
		return p == nil || now.Sub(p.Seen) > runTimeout
	}
	for _, j := range b.order {
		switch {
		case j.State == "running" && now.Sub(j.Beat) > runTimeout:
			b.finishLocked(j, false, nil, "the Figma plugin stopped responding while running this script — was its panel closed, or did the script call figma.closePlugin()?", "")
		case j.State == "queued" && j.Plugin != "" && gone(j.Plugin):
			b.dropLocked(j, "expired", fmt.Sprintf("Marionette was closed in the Figma file %q before this script ran, so it was not run", j.File))
		}
	}
	for id := range b.plugins {
		if gone(id) {
			delete(b.plugins, id)
		}
	}
}

// handlePull serves one plugin, which reports its file on every poll.
func (b *bridge) handlePull(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id := q.Get("plugin")
	if id == "" || len(id) > 64 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "this copy of the plugin is out of date — close it and run Plugins → Development → Marionette again"})
		return
	}
	now := time.Now()
	var pulled map[string]string
	b.mu.Lock()
	p := b.plugins[id]
	if p == nil {
		p = &plugin{ID: id, First: now}
		b.plugins[id] = p
	}
	p.File, p.Key, p.Page, p.Seen = q.Get("file"), q.Get("key"), q.Get("page"), now
	if busy := q.Get("busy"); busy != "" {
		if j := b.jobs[busy]; j != nil && j.State == "running" {
			j.Beat = now
		}
	} else if j := b.nextLocked(p, now); j != nil {
		j.State = "running"
		j.Plugin, j.File = p.ID, p.File
		j.Started, j.Beat = now, now
		pulled = map[string]string{"id": j.ID, "code": j.Code}
		j.Code = ""
	}
	b.mu.Unlock()
	if pulled == nil {
		writeJSON(w, http.StatusOK, map[string]any{"job": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": pulled})
}

// nextLocked dequeues the first job plugin p may run: one meant for it, or
// one meant for no file in particular that pickLocked now gives p. With
// several files open, such a job is refused rather than run in whichever
// polled first. Caller holds b.mu.
func (b *bridge) nextLocked(p *plugin, now time.Time) *job {
	for i := 0; i < len(b.queue); i++ {
		j := b.queue[i]
		if j.Plugin == "" {
			pick, err := b.pickLocked("", now)
			if err != nil {
				b.dropLocked(j, "refused", err.Error())
				i--
				continue
			}
			if pick != p {
				continue
			}
		} else if j.Plugin != p.ID {
			continue
		}
		b.queue = slices.Delete(b.queue, i, i+1)
		return j
	}
	return nil
}

func (b *bridge) handlePostResult(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID     string          `json:"id"`
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
		Stack  string          `json:"stack"`
	}
	decodeErr := json.NewDecoder(http.MaxBytesReader(w, r.Body, b.maxResult)).Decode(&body)
	// The id rides in the URL so an unreadable body can still fail its job.
	id := r.URL.Query().Get("id")
	if id == "" {
		id = body.ID
	}
	b.mu.Lock()
	j := b.jobs[id]
	b.mu.Unlock()
	if j == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown job id"})
		return
	}

	var result any
	ok, errMsg, stack := body.OK, body.Error, body.Stack
	if decodeErr != nil {
		ok, stack = false, ""
		errMsg = "the plugin's result could not be read: " + decodeErr.Error()
		var tooBig *http.MaxBytesError
		if errors.As(decodeErr, &tooBig) {
			errMsg = fmt.Sprintf("the result is larger than %d MB — return less data or fewer screenshots", b.maxResult>>20)
		}
	} else if ok && len(body.Result) > 0 {
		dec := json.NewDecoder(bytes.NewReader(body.Result))
		dec.UseNumber()
		if err := dec.Decode(&result); err == nil {
			n := 0
			if result, err = b.savePNGs(result, j.ID[:8], &n); err != nil {
				ok, errMsg, stack, result = false, "saving screenshot: "+err.Error(), "", nil
			}
		}
	}

	b.mu.Lock()
	recorded := b.finishLocked(j, ok, result, errMsg, stack)
	b.mu.Unlock()
	switch {
	case !recorded:
		writeJSON(w, http.StatusConflict, map[string]any{"error": "this job is not running"})
	case decodeErr != nil:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": errMsg})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// savePNGs replaces every {$png: base64} value (from helpers.shot) with the
// path of a PNG file written to pngDir, so agents get viewable files.
func (b *bridge) savePNGs(v any, prefix string, n *int) (any, error) {
	switch v := v.(type) {
	case []any:
		for i := range v {
			out, err := b.savePNGs(v[i], prefix, n)
			if err != nil {
				return nil, err
			}
			v[i] = out
		}
		return v, nil
	case map[string]any:
		if s, ok := v["$png"].(string); ok {
			data, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return nil, err
			}
			if err := os.MkdirAll(b.pngDir, 0o700); err != nil {
				return nil, err
			}
			path := filepath.Join(b.pngDir, fmt.Sprintf("%s-%d.png", prefix, *n))
			*n++
			return path, os.WriteFile(path, data, 0o600)
		}
		for k := range v {
			out, err := b.savePNGs(v[k], prefix, n)
			if err != nil {
				return nil, err
			}
			v[k] = out
		}
		return v, nil
	}
	return v, nil
}

func (b *bridge) handleHealth(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	files := b.openFilesLocked(time.Now())
	body := map[string]any{
		"ok": true, "app": "marionette", "version": version,
		"plugin_connected": len(files) > 0, "files": files, "queued": len(b.queue),
	}
	b.mu.Unlock()
	writeJSON(w, http.StatusOK, body)
}

func (b *bridge) handleStatus(w http.ResponseWriter, r *http.Request) {
	type jobView struct {
		ID      string `json:"id"`
		Summary string `json:"summary"`
		File    string `json:"file,omitempty"`
		State   string `json:"state"`
		OK      bool   `json:"ok"`
		Error   string `json:"error,omitempty"`
		At      int64  `json:"at"`
		MS      int64  `json:"ms"`
	}
	b.mu.Lock()
	jobs := []jobView{}
	for i := len(b.order) - 1; i >= 0 && len(jobs) < 20; i-- {
		j := b.order[i]
		v := jobView{ID: j.ID[:8], Summary: j.Summary, File: j.File, State: j.State, OK: j.OK, Error: j.Error, At: j.Queued.UnixMilli()}
		if !j.Ended.IsZero() && !j.Started.IsZero() {
			v.MS = j.Ended.Sub(j.Started).Milliseconds()
		}
		jobs = append(jobs, v)
	}
	files := b.openFilesLocked(time.Now())
	b.mu.Unlock()

	_, err := os.Stat(b.skillPath)
	figma := figmaSettingsPath()
	_, figmaErr := os.Stat(figma)
	writeJSON(w, http.StatusOK, map[string]any{
		"figma_found":      figmaErr == nil,
		"figma_registered": figmaErr == nil && figmaRegistered(figma, b.pluginDir),
		"version":          version,
		"port":             b.port,
		"os":               goos,
		"plugin_connected": len(files) > 0,
		"files":            files,
		"plugin_manifest":  filepath.Join(b.pluginDir, "manifest.json"),
		"skill_path":       b.skillPath,
		"skill_installed":  b.skillPath != "" && err == nil,
		"jobs":             jobs,
	})
}

// readScript accepts the raw script as the body, or {"code": "..."} JSON.
func readScript(r *http.Request) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxScriptBytes+1))
	if err != nil {
		return "", err
	}
	if len(raw) > maxScriptBytes {
		return "", fmt.Errorf("script larger than %d bytes", maxScriptBytes)
	}
	code := string(raw)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return "", fmt.Errorf("invalid JSON: %v", err)
		}
		code = body.Code
	}
	if strings.TrimSpace(code) == "" {
		return "", fmt.Errorf("empty script: send the JavaScript as the request body")
	}
	return code, nil
}

func parseWait(r *http.Request) (time.Duration, error) {
	s := r.URL.Query().Get("wait")
	if s == "" {
		return 0, nil
	}
	sec, err := strconv.ParseFloat(s, 64)
	if err != nil || sec < 0 {
		return 0, fmt.Errorf("wait must be a number of seconds")
	}
	return min(time.Duration(sec*float64(time.Second)), maxWait), nil
}

// summarize labels a job in the status window by its first meaningful line.
func summarize(code string) string {
	for _, line := range strings.Split(code, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "//"))
		if line != "" {
			if r := []rune(line); len(r) > 80 {
				return string(r[:79]) + "…"
			}
			return line
		}
	}
	return "(empty)"
}

func newID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40 // UUID v4
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(body)
}
