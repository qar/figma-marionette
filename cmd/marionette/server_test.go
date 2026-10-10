package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "test-token-0123456789abcdef0123456789abcdef"

var pluginHeader = map[string]string{"X-Marionette-Token": testToken}

func startBridge(t *testing.T) (*bridge, string) {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	b := newBridge(ts.Listener.Addr().(*net.TCPAddr).Port, testToken, t.TempDir())
	b.started = time.Time{} // settled long ago
	ts.Config.Handler = b.handler()
	ts.Start()
	t.Cleanup(ts.Close)
	return b, ts.URL
}

func do(t *testing.T, method, url, body string, header map[string]string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		if k == "Host" {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s %s: non-JSON response %q", method, url, raw)
	}
	return res.StatusCode, out
}

// pullURL is a poll from the plugin with this id, running in file. Its file
// key is "key-" + plugin.
func pullURL(base, plugin, file string) string {
	return base + "/pull?" + url.Values{"plugin": {plugin}, "file": {file}, "key": {"key-" + plugin}, "page": {"Page 1"}}.Encode()
}

// poll plays one poll of a plugin and returns the job it got, if any.
func poll(t *testing.T, base, plugin, file string) map[string]any {
	t.Helper()
	_, out := do(t, "GET", pullURL(base, plugin, file), "", pluginHeader)
	job, _ := out["job"].(map[string]any)
	return job
}

// pull plays the plugin: it takes the next job and returns its id and code.
func pull(t *testing.T, base string) (string, string) {
	t.Helper()
	return pullAs(t, base, "p1", "File 1")
}

func pullAs(t *testing.T, base, plugin, file string) (string, string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if job := poll(t, base, plugin, file); job != nil {
			return job["id"].(string), job["code"].(string)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("plugin never received a job")
	return "", ""
}

func postResult(t *testing.T, base, id, body string) int {
	t.Helper()
	h := map[string]string{"X-Marionette-Token": testToken, "Content-Type": "application/json"}
	status, _ := do(t, "POST", base+"/result?id="+id, body, h)
	return status
}

func submit(t *testing.T, base, code string) string {
	t.Helper()
	status, out := do(t, "POST", base+"/run", code, nil)
	if status != http.StatusOK {
		t.Fatalf("submit: status %d, body %v", status, out)
	}
	return out["id"].(string)
}

func TestRunWaitRoundTripSavesScreenshots(t *testing.T) {
	_, base := startBridge(t)
	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG fake"))
	go func() {
		id, code := pull(t, base)
		if code != "return 1;" {
			t.Errorf("plugin got code %q", code)
		}
		postResult(t, base, id, `{"ok":true,"result":{"n":1.5,"shots":[{"$png":"`+png+`"}]}}`)
	}()

	status, out := do(t, "POST", base+"/run?wait=5", "return 1;", nil)
	if status != http.StatusOK || out["ok"] != true {
		t.Fatalf("status %d, body %v", status, out)
	}
	result := out["result"].(map[string]any)
	if result["n"] != 1.5 {
		t.Errorf("number not preserved: %v", result["n"])
	}
	path := result["shots"].([]any)[0].(string)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "\x89PNG fake" {
		t.Errorf("screenshot at %q: %q, %v", path, data, err)
	}
}

func TestScriptErrorIs422(t *testing.T) {
	_, base := startBridge(t)
	go func() {
		id, _ := pull(t, base)
		postResult(t, base, id, `{"ok":false,"error":"boom","stack":"at x"}`)
	}()
	status, out := do(t, "POST", base+"/run?wait=5", "throw new Error('boom')", nil)
	if status != http.StatusUnprocessableEntity || out["error"] != "boom" || out["stack"] != "at x" {
		t.Fatalf("status %d, body %v", status, out)
	}
}

func TestSubmitterTimeoutDiscardsTheQueuedJob(t *testing.T) {
	_, base := startBridge(t)
	status, out := do(t, "POST", base+"/run?wait=0.2", "return 1;", nil)
	if status != http.StatusGatewayTimeout || out["state"] != "expired" {
		t.Fatalf("status %d, body %v", status, out)
	}
	// A plugin connecting later must not run the abandoned script.
	if job := poll(t, base, "p1", "File 1"); job != nil {
		t.Fatalf("expired job was still handed out: %v", job)
	}
}

func TestPollTimeoutKeepsTheJob(t *testing.T) {
	_, base := startBridge(t)
	id := submit(t, base, "return 2;")
	status, out := do(t, "GET", base+"/result?id="+id+"&wait=0.2", "", nil)
	if status != http.StatusOK || out["pending"] != true || out["state"] != "queued" {
		t.Fatalf("poll timeout: status %d, body %v", status, out)
	}
	pulledID, _ := pull(t, base)
	if pulledID != id {
		t.Fatalf("pulled %s, want the polled job %s", pulledID, id)
	}
	postResult(t, base, id, `{"ok":true,"result":2}`)
	if _, out := do(t, "GET", base+"/result?id="+id, "", nil); out["result"] != 2.0 {
		t.Fatalf("expected result 2, got %v", out)
	}
}

func TestDuplicateResultIsRejectedWithoutDeadlock(t *testing.T) {
	_, base := startBridge(t)
	id := submit(t, base, "return 1;")
	pull(t, base)

	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i := range statuses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i] = postResult(t, base, id, `{"ok":true,"result":1}`)
		}()
	}
	wg.Wait()
	if !(statuses[0] == http.StatusOK && statuses[1] == http.StatusConflict) &&
		!(statuses[0] == http.StatusConflict && statuses[1] == http.StatusOK) {
		t.Fatalf("want one 200 and one 409, got %v", statuses)
	}
	if status, _ := do(t, "GET", base+"/health", "", nil); status != http.StatusOK {
		t.Fatalf("bridge unresponsive after duplicate result: %d", status)
	}
}

func TestSilentPluginFailsTheRunningJob(t *testing.T) {
	b, base := startBridge(t)
	id := submit(t, base, "return 1;")
	pull(t, base)

	do(t, "GET", pullURL(base, "p1", "File 1")+"&busy="+id, "", pluginHeader) // heartbeat
	b.reap(time.Now().Add(runTimeout / 2))
	if _, out := do(t, "GET", base+"/result?id="+id, "", nil); out["state"] != "running" {
		t.Fatalf("job reaped despite a fresh heartbeat: %v", out)
	}

	b.reap(time.Now().Add(runTimeout + time.Second))
	status, out := do(t, "GET", base+"/result?id="+id, "", nil)
	if status != http.StatusUnprocessableEntity || !strings.Contains(out["error"].(string), "stopped responding") {
		t.Fatalf("status %d, body %v", status, out)
	}
}

func TestOversizedResultFailsTheJob(t *testing.T) {
	b, base := startBridge(t)
	b.maxResult = 64
	id := submit(t, base, "return 1;")
	pull(t, base)
	if status := postResult(t, base, id, `{"ok":true,"result":"`+strings.Repeat("x", 200)+`"}`); status != http.StatusBadRequest {
		t.Fatalf("oversized result: status %d, want 400", status)
	}
	status, out := do(t, "GET", base+"/result?id="+id, "", nil)
	if status != http.StatusUnprocessableEntity || !strings.Contains(out["error"].(string), "larger than") {
		t.Fatalf("status %d, body %v", status, out)
	}
}

func TestRequestsWebPagesCouldSendAreRefused(t *testing.T) {
	_, base := startBridge(t)
	cases := []struct {
		name   string
		method string
		path   string
		header map[string]string
		want   int
	}{
		{"cross-origin run", "POST", "/run", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"no-cors run", "POST", "/run", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"rebinding host", "GET", "/health", map[string]string{"Host": "evil.example:80"}, http.StatusForbidden},
		{"pull without token", "GET", "/pull", map[string]string{"Origin": "null"}, http.StatusUnauthorized},
		{"pull with wrong token", "GET", "/pull", map[string]string{"Origin": "null", "X-Marionette-Token": "nope"}, http.StatusUnauthorized},
		{"result without token", "POST", "/result?id=x", nil, http.StatusUnauthorized},
		{"plugin pull", "GET", "/pull?plugin=p1", map[string]string{"Origin": "null", "X-Marionette-Token": testToken}, http.StatusOK},
		{"pull from an outdated plugin", "GET", "/pull", map[string]string{"Origin": "null", "X-Marionette-Token": testToken}, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if status, _ := do(t, c.method, base+c.path, "return 1;", c.header); status != c.want {
				t.Errorf("status %d, want %d", status, c.want)
			}
		})
	}
}

func TestPluginConnectionFollowsPolling(t *testing.T) {
	b, base := startBridge(t)
	if _, h := do(t, "GET", base+"/health", "", nil); h["plugin_connected"] != false {
		t.Fatalf("connected before any pull: %v", h)
	}
	poll(t, base, "p1", "File 1")
	_, h := do(t, "GET", base+"/health", "", nil)
	if files := h["files"].([]any); h["plugin_connected"] != true || len(files) != 1 || files[0].(map[string]any)["name"] != "File 1" {
		t.Fatalf("not connected right after a pull: %v", h)
	}
	b.mu.Lock()
	b.plugins["p1"].Seen = time.Now().Add(-time.Minute)
	b.mu.Unlock()
	if _, h := do(t, "GET", base+"/health", "", nil); h["plugin_connected"] != false || len(h["files"].([]any)) != 0 {
		t.Fatalf("still connected after polling stopped: %v", h)
	}
}

func TestScriptGoesToTheOnlyOpenFile(t *testing.T) {
	_, base := startBridge(t)
	poll(t, base, "p1", "File 1")
	_, out := do(t, "POST", base+"/run", "return 1;", nil)
	if file, _ := out["file"].(map[string]any); file["name"] != "File 1" {
		t.Fatalf("submit did not name the file it will run in: %v", out)
	}
	// A file that opens later must not take a script meant for File 1.
	if job := poll(t, base, "p2", "File 2"); job != nil {
		t.Fatalf("File 2 took File 1's script: %v", job)
	}
	id, _ := pullAs(t, base, "p1", "File 1")
	postResult(t, base, id, `{"ok":true,"result":1}`)
	_, out = do(t, "GET", base+"/result?id="+id, "", nil)
	if file, _ := out["file"].(map[string]any); file["id"] != "p1" || file["name"] != "File 1" {
		t.Fatalf("outcome does not name the file it ran in: %v", out)
	}
}

func TestSeveralOpenFilesNeedAChoice(t *testing.T) {
	_, base := startBridge(t)
	poll(t, base, "p1", "File 1")
	poll(t, base, "p2", "File 2")
	status, out := do(t, "POST", base+"/run?wait=5", "return 1;", nil)
	if status != http.StatusConflict || out["state"] != "choose_file" {
		t.Fatalf("status %d, body %v", status, out)
	}
	files := out["files"].([]any)
	if len(files) != 2 || files[0].(map[string]any)["name"] != "File 1" || files[1].(map[string]any)["id"] != "p2" {
		t.Fatalf("files not listed in connection order: %v", files)
	}
	for _, p := range []string{"p1", "p2"} {
		if job := poll(t, base, p, "File "+p[1:]); job != nil {
			t.Fatalf("refused script was handed to %s: %v", p, job)
		}
	}
}

func TestFileParamPicksTheFile(t *testing.T) {
	for _, want := range []string{"p2", "File 2", "key-p2"} {
		t.Run(want, func(t *testing.T) {
			_, base := startBridge(t)
			poll(t, base, "p1", "File 1")
			poll(t, base, "p2", "File 2")
			status, out := do(t, "POST", base+"/run?file="+url.QueryEscape(want), "return 1;", nil)
			if status != http.StatusOK {
				t.Fatalf("status %d, body %v", status, out)
			}
			if job := poll(t, base, "p1", "File 1"); job != nil {
				t.Fatalf("File 1 took a script meant for File 2: %v", job)
			}
			if id, _ := pullAs(t, base, "p2", "File 2"); id != out["id"] {
				t.Fatalf("File 2 pulled %s, want %s", id, out["id"])
			}
		})
	}
}

func TestUnmatchedOrAmbiguousFileIsRefused(t *testing.T) {
	_, base := startBridge(t)
	poll(t, base, "p1", "Same name")
	poll(t, base, "p2", "Same name")
	for want, msg := range map[string]string{"Other": "no Figma file", "Same name": "2 open Figma files match"} {
		status, out := do(t, "POST", base+"/run?file="+url.QueryEscape(want), "return 1;", nil)
		if status != http.StatusConflict || out["state"] != "choose_file" || !strings.Contains(out["error"].(string), msg) {
			t.Errorf("file=%q: status %d, body %v", want, status, out)
		}
	}
}

func TestReopenedPluginStandsForItsFile(t *testing.T) {
	b, base := startBridge(t)
	poll(t, base, "old", "File 1")
	time.Sleep(time.Millisecond) // the reopened plugin connects later
	poll(t, base, "new", "File 1")
	b.mu.Lock()
	b.plugins["new"].Key = b.plugins["old"].Key // same file
	b.mu.Unlock()
	if _, h := do(t, "GET", base+"/health", "", nil); len(h["files"].([]any)) != 1 {
		t.Errorf("one file listed twice: %v", h["files"])
	}
	for _, want := range []string{"", "File 1", "key-old"} {
		status, out := do(t, "POST", base+"/run?file="+url.QueryEscape(want), "return 1;", nil)
		if file, _ := out["file"].(map[string]any); status != http.StatusOK || file["id"] != "new" {
			t.Errorf("file=%q: status %d, body %v", want, status, out)
		}
	}
}

func TestWaitingScriptIsRefusedWhenSeveralFilesOpen(t *testing.T) {
	b, base := startBridge(t)
	id := submit(t, base, "return 1;") // no file open yet: waits for one
	now := time.Now()
	b.mu.Lock()
	b.plugins["p2"] = &plugin{ID: "p2", File: "File 2", First: now, Seen: now}
	b.mu.Unlock()
	if job := poll(t, base, "p1", "File 1"); job != nil {
		t.Fatalf("a script naming no file ran with two files open: %v", job)
	}
	status, out := do(t, "GET", base+"/result?id="+id, "", nil)
	if status != http.StatusConflict || out["state"] != "choose_file" || len(out["files"].([]any)) != 2 {
		t.Fatalf("status %d, body %v", status, out)
	}
}

func TestScriptForAClosedFileExpires(t *testing.T) {
	b, base := startBridge(t)
	poll(t, base, "p1", "File 1")
	id := submit(t, base, "return 1;")
	b.reap(time.Now().Add(runTimeout + time.Second))
	status, out := do(t, "GET", base+"/result?id="+id, "", nil)
	if status != http.StatusGatewayTimeout || out["state"] != "expired" || !strings.Contains(out["error"].(string), "File 1") {
		t.Fatalf("status %d, body %v", status, out)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.plugins) != 0 {
		t.Fatalf("closed plugin not forgotten: %v", b.plugins)
	}
}

func TestRunWaitsForPluginsAfterStart(t *testing.T) {
	b, base := startBridge(t)
	b.started = time.Now()
	poll(t, base, "p1", "File 1")
	go func() {
		time.Sleep(settle / 2)
		poll(t, base, "p2", "File 2")
	}()
	// Right after a start only File 1 has polled; File 2 polls moments later.
	if status, out := do(t, "POST", base+"/run", "return 1;", nil); status != http.StatusConflict {
		t.Fatalf("picked a file before every plugin had polled: status %d, body %v", status, out)
	}
}

func TestSummarize(t *testing.T) {
	cases := map[string]string{
		"\n  // Build the settings screen\nconst x = 1;": "Build the settings screen",
		"return 1;":              "return 1;",
		"   \n\n":                "(empty)",
		strings.Repeat("a", 100): strings.Repeat("a", 79) + "…",
	}
	for in, want := range cases {
		if got := summarize(in); got != want {
			t.Errorf("summarize(%q) = %q, want %q", in, got, want)
		}
	}
}
