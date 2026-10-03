package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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

// pull plays the plugin: it takes the next job and returns its id and code.
func pull(t *testing.T, base string) (string, string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, out := do(t, "GET", base+"/pull", "", pluginHeader)
		if job, ok := out["job"].(map[string]any); ok {
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
	if _, pulled := do(t, "GET", base+"/pull", "", pluginHeader); pulled["job"] != nil {
		t.Fatalf("expired job was still handed out: %v", pulled)
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

	do(t, "GET", base+"/pull?busy="+id, "", pluginHeader) // heartbeat
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
		{"plugin pull", "GET", "/pull", map[string]string{"Origin": "null", "X-Marionette-Token": testToken}, http.StatusOK},
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
	do(t, "GET", base+"/pull", "", pluginHeader)
	if _, h := do(t, "GET", base+"/health", "", nil); h["plugin_connected"] != true {
		t.Fatalf("not connected right after a pull: %v", h)
	}
	b.mu.Lock()
	b.lastPull = time.Now().Add(-time.Minute)
	b.mu.Unlock()
	if _, h := do(t, "GET", base+"/health", "", nil); h["plugin_connected"] != false {
		t.Fatalf("still connected after polling stopped: %v", h)
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
