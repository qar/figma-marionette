package main

// The bridge: agents queue scripts, the Figma plugin pulls and runs them.
//
//   agent   POST /run?wait=N   script body           -> outcome (or {id} when wait=0)
//   agent   GET  /result?id=&wait=N                  -> outcome | {pending: true}
//   plugin  GET  /pull                               -> {job: {id, code} | null}
//   plugin  GET  /pull?busy=<id>                     -> heartbeat while a script runs
//   plugin  POST /result?id=<id>  {ok, result, ...}  -> {ok: true}
//   any     GET  /health                             -> liveness + plugin connection
//   window  GET  /  and  /status                     -> the status UI
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
	// A running script whose plugin stops heartbeating is declared lost.
	runTimeout = 15 * time.Second
)

type job struct {
	ID      string
	Code    string // dropped once the plugin has pulled it
	Summary string
	State   string // queued | running | done | expired
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

	mu       sync.Mutex
	queue    []*job
	jobs     map[string]*job
	order    []*job // oldest first
	lastPull time.Time
}

func newBridge(port int, token, pngDir string) *bridge {
	b := &bridge{port: port, token: token, pngDir: pngDir, maxResult: maxResultBytes, jobs: map[string]*job{}}
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
	j, queued := b.enqueue(code)
	if wait == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"id": j.ID, "queued": queued})
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
	case j.OK:
		code, body = http.StatusOK, map[string]any{"ok": true, "id": j.ID, "result": j.Result, "ms": j.Ended.Sub(j.Started).Milliseconds()}
	default:
		code, body = http.StatusUnprocessableEntity, map[string]any{"ok": false, "id": j.ID, "error": j.Error, "stack": j.Stack}
	}
	b.mu.Unlock()
	writeJSON(w, code, body)
}

func (b *bridge) enqueue(code string) (*job, int) {
	j := &job{
		ID:      newID(),
		Code:    code,
		Summary: summarize(code),
		State:   "queued",
		Queued:  time.Now(),
		done:    make(chan struct{}),
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.queue = append(b.queue, j)
	b.jobs[j.ID] = j
	b.order = append(b.order, j)
	b.prune()
	return j, len(b.queue)
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
	if j.State != "queued" {
		return
	}
	for i, q := range b.queue {
		if q == j {
			b.queue = append(b.queue[:i], b.queue[i+1:]...)
			break
		}
	}
	j.State = "expired"
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
// script called figma.closePlugin(), or the result never made it back.
func (b *bridge) reap(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, j := range b.order {
		if j.State == "running" && now.Sub(j.Beat) > runTimeout {
			b.finishLocked(j, false, nil, "the Figma plugin stopped responding while running this script — was its panel closed, or did the script call figma.closePlugin()?", "")
		}
	}
}

func (b *bridge) handlePull(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	var pulled map[string]string
	b.mu.Lock()
	b.lastPull = now
	if id := r.URL.Query().Get("busy"); id != "" {
		if j := b.jobs[id]; j != nil && j.State == "running" {
			j.Beat = now
		}
	} else if len(b.queue) > 0 {
		j := b.queue[0]
		b.queue = b.queue[1:]
		j.State = "running"
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

// pluginConnected reports whether the plugin polled or heartbeat recently.
// Caller holds b.mu.
func (b *bridge) pluginConnected() bool {
	return time.Since(b.lastPull) < pluginTimeout
}

func (b *bridge) handleHealth(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	body := map[string]any{
		"ok": true, "app": "marionette", "version": version,
		"plugin_connected": b.pluginConnected(), "queued": len(b.queue),
	}
	b.mu.Unlock()
	writeJSON(w, http.StatusOK, body)
}

func (b *bridge) handleStatus(w http.ResponseWriter, r *http.Request) {
	type jobView struct {
		ID      string `json:"id"`
		Summary string `json:"summary"`
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
		v := jobView{ID: j.ID[:8], Summary: j.Summary, State: j.State, OK: j.OK, Error: j.Error, At: j.Queued.UnixMilli()}
		if !j.Ended.IsZero() && !j.Started.IsZero() {
			v.MS = j.Ended.Sub(j.Started).Milliseconds()
		}
		jobs = append(jobs, v)
	}
	connected := b.pluginConnected()
	b.mu.Unlock()

	_, err := os.Stat(b.skillPath)
	writeJSON(w, http.StatusOK, map[string]any{
		"version":          version,
		"port":             b.port,
		"os":               goos,
		"plugin_connected": connected,
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
