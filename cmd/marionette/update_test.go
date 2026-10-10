package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.4.0", "v0.3.1", true},
		{"v0.10.0", "v0.9.9", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.3.1", "v0.3.1", false},
		{"v0.3.0", "v0.3.1", false},
		{"v0.3.2", "v0.3.1-4-gabcdef-dirty", true}, // a build past v0.3.1
		{"v0.3.1", "v0.3.1-4-gabcdef-dirty", false},
		{"v0.4.0-rc1", "v0.3.1", false}, // pre-releases are never offered
		{"v0.4.0", "dev", false},
		{"v0.4.0", "abcdef0", false},
		{"", "v0.3.1", false},
		{"v0.4", "v0.3.1", false},
	}
	for _, c := range cases {
		if got := newer(c.latest, c.current); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestScutilProxy(t *testing.T) {
	const both = `<dictionary> {
  ExceptionsList : <array> {
    0 : 127.0.0.1
    1 : *.local
  }
  HTTPEnable : 1
  HTTPPort : 7897
  HTTPProxy : 127.0.0.1
  HTTPSEnable : 1
  HTTPSPort : 7897
  HTTPSProxy : 127.0.0.1
  SOCKSEnable : 1
  SOCKSPort : 7898
  SOCKSProxy : 127.0.0.1
}`
	cases := map[string]string{
		both: "http://127.0.0.1:7897",
		strings.ReplaceAll(both, "HTTPSEnable : 1", "HTTPSEnable : 0"): "socks5://127.0.0.1:7898",
		"<dictionary> {\n  FTPPassive : 1\n}":                          "",
	}
	for out, want := range cases {
		got := ""
		if u := scutilProxy(out); u != nil {
			got = u.String()
		}
		if got != want {
			t.Errorf("scutilProxy = %q, want %q for\n%s", got, want, out)
		}
	}
}

func TestAppBundle(t *testing.T) {
	cases := map[string]string{
		"/Applications/Marionette.app/Contents/MacOS/marionette":                         "/Applications/Marionette.app",
		"/Users/me/dist/Marionette.app/Contents/MacOS/marionette":                        "/Users/me/dist/Marionette.app",
		"/private/var/folders/x/AppTranslocation/1234/d/Marionette.app/Contents/MacOS/m": "",
		"/Users/me/repo/dist/marionette":                                                 "",
		"/tmp/Contents/MacOS/marionette":                                                 "",
	}
	for exe, want := range cases {
		if got := appBundle(exe); got != want {
			t.Errorf("appBundle(%q) = %q, want %q", exe, got, want)
		}
	}
}

// releases serves a fake GitHub releases area: /releases/latest redirects to
// tag latest, and the macOS download is the zip file archive.
func releases(t *testing.T, latest, archive string) *updater {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /x/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if latest == "" {
			http.Redirect(w, r, "/x/releases", http.StatusFound)
			return
		}
		http.Redirect(w, r, "https://github.com/x/releases/tag/"+latest, http.StatusFound)
	})
	mux.HandleFunc("GET /x/releases/download/{tag}/Marionette-macos.zip", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("tag") != latest || archive == "" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, archive)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	u := newUpdater("v0.3.1", "")
	u.base, u.client = ts.URL+"/x/releases", ts.Client()
	return u
}

func TestCheckFindsTheLatestRelease(t *testing.T) {
	u := releases(t, "v0.4.0", "")
	u.check()
	v := u.view()
	if v["latest"] != "v0.4.0" || v["available"] != true || v["checks"] != 1 || v["check_error"] != "" {
		t.Fatalf("view after check: %v", v)
	}
	if v["url"] != u.base+"/tag/v0.4.0" {
		t.Errorf("release page %v", v["url"])
	}

	none := releases(t, "", "")
	none.check()
	if v := none.view(); v["available"] != false || !strings.Contains(v["check_error"].(string), "unexpected answer") {
		t.Fatalf("view without releases: %v", v)
	}
}

// fakeApp writes a Marionette.app under dir whose binary prints version.
func fakeApp(t *testing.T, dir, version string) string {
	t.Helper()
	app := filepath.Join(dir, "Marionette.app")
	bin := filepath.Join(app, "Contents", "MacOS", "marionette")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho "+version+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return app
}

// zipApp packs an app that prints version the way `make release` does.
func zipApp(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	app := fakeApp(t, dir, version)
	archive := filepath.Join(dir, "Marionette-macos.zip")
	if out, err := exec.Command("ditto", "-c", "-k", "--keepParent", app, archive).CombinedOutput(); err != nil {
		t.Fatalf("ditto: %v: %s", err, out)
	}
	return archive
}

func installedVersion(t *testing.T, app string) string {
	t.Helper()
	out, err := exec.Command(filepath.Join(app, "Contents", "MacOS", "marionette"), "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func stubRestart(t *testing.T) *atomic.Int32 {
	t.Helper()
	var restarts atomic.Int32
	oldRelaunch, oldExit := relaunch, exit
	relaunch = func(string) error { restarts.Add(1); return nil }
	exit = func(int) {}
	t.Cleanup(func() { relaunch, exit = oldRelaunch, oldExit })
	return &restarts
}

func TestInstallReplacesTheAppAndRestarts(t *testing.T) {
	if _, err := exec.LookPath("ditto"); err != nil || goos != "darwin" {
		t.Skip("needs macOS")
	}
	restarts := stubRestart(t)
	u := releases(t, "v0.4.0", zipApp(t, "v0.4.0"))
	apps := t.TempDir()
	u.bundle = fakeApp(t, apps, "v0.3.1")
	u.check()
	u.install()

	if got := installedVersion(t, u.bundle); got != "v0.4.0" {
		t.Fatalf("installed app reports %q", got)
	}
	if entries, _ := os.ReadDir(apps); len(entries) != 1 {
		t.Errorf("leftovers next to the app: %v", entries)
	}
	if v := u.view(); v["state"] != "restarting" || restarts.Load() != 1 {
		t.Fatalf("state %v after %d restarts", v, restarts.Load())
	}
}

func TestInstallKeepsTheAppWhenTheDownloadIsWrong(t *testing.T) {
	if _, err := exec.LookPath("ditto"); err != nil || goos != "darwin" {
		t.Skip("needs macOS")
	}
	restarts := stubRestart(t)
	u := releases(t, "v0.4.0", zipApp(t, "v0.3.9")) // not the version it claims
	u.bundle = fakeApp(t, t.TempDir(), "v0.3.1")
	u.check()
	u.install()

	if got := installedVersion(t, u.bundle); got != "v0.3.1" {
		t.Fatalf("app was replaced by a bad download: %q", got)
	}
	v := u.view()
	if v["state"] != "" || !strings.Contains(v["install_error"].(string), "does not run as v0.4.0") || restarts.Load() != 0 {
		t.Fatalf("view %v after %d restarts", v, restarts.Load())
	}
}

func TestNothingToInstallWithoutANewerRelease(t *testing.T) {
	restarts := stubRestart(t)
	u := releases(t, "v0.3.1", "")
	u.bundle = "/nonexistent/Marionette.app"
	u.check()
	u.install()
	if v := u.view(); v["available"] != false || v["state"] != "" || restarts.Load() != 0 {
		t.Fatalf("view %v after %d restarts", v, restarts.Load())
	}
}

func TestRefreshSkill(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	if err := refreshSkill(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("refresh installed a skill the user never installed: %v", err)
	}
	os.WriteFile(path, []byte("old skill"), 0o644)
	before := time.Now()
	if err := refreshSkill(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if !strings.Contains(string(data), "Pick the file first") || info.ModTime().Before(before.Add(-time.Second)) {
		t.Fatalf("skill not refreshed: %.40q", data)
	}
}
