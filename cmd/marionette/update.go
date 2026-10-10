package main

// Updates: the app looks for a newer GitHub release at start and once a day,
// and on macOS can install it in place and restart. The newest tag comes from
// the redirect behind /releases/latest, which unlike the GitHub API has no
// rate limit. A download made by the app itself carries no quarantine flag,
// so the new version opens without the Gatekeeper prompt a browser download
// would bring.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const releasesURL = "https://github.com/qar/figma-marionette/releases"

type updater struct {
	current string
	base    string       // releasesURL; tests point it elsewhere
	bundle  string       // the .app to replace; "" when updating in place is impossible
	client  *http.Client // nil: proxies from the environment and the system

	mu         sync.Mutex
	latest     string // newest release tag, once a check has succeeded
	checks     int    // completed checks, so the window can tell when one ends
	state      string // "", "checking", "installing" or "restarting"
	checkErr   string
	installErr string
}

func newUpdater(current, bundle string) *updater {
	return &updater{current: current, base: releasesURL, bundle: bundle}
}

// run checks shortly after start and then daily. Development builds, which
// have no release version, never check.
func (u *updater) run() {
	if _, ok := parseVersion(u.current); !ok {
		return
	}
	time.Sleep(5 * time.Second)
	for {
		u.check()
		time.Sleep(24 * time.Hour)
	}
}

// check looks up the newest release, unless something else is under way.
func (u *updater) check() {
	u.mu.Lock()
	if u.state != "" {
		u.mu.Unlock()
		return
	}
	u.state = "checking"
	u.mu.Unlock()

	tag, err := u.latestRelease()
	u.mu.Lock()
	defer u.mu.Unlock()
	u.state = ""
	u.checks++
	u.checkErr = ""
	if err != nil {
		u.checkErr = err.Error()
		return
	}
	u.latest = tag
}

func (u *updater) latestRelease() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", u.base+"/latest", nil)
	if err != nil {
		return "", err
	}
	c := u.httpClient()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("cannot reach GitHub: %w", err)
	}
	res.Body.Close()
	loc := res.Header.Get("Location")
	i := strings.LastIndex(loc, "/releases/tag/")
	if res.StatusCode/100 != 3 || i < 0 {
		return "", fmt.Errorf("unexpected answer from GitHub: %s", res.Status)
	}
	tag, err := url.PathUnescape(loc[i+len("/releases/tag/"):])
	if v, ok := parseVersion(tag); !ok || v.pre {
		return "", fmt.Errorf("unexpected release tag %q", tag)
	}
	return tag, nil
}

// install starts installing the newest release, then restarts into it.
func (u *updater) install() {
	u.mu.Lock()
	tag := u.latest
	if u.state != "" || !u.canInstallLocked() || !newer(tag, u.current) {
		u.mu.Unlock()
		return
	}
	u.state = "installing"
	u.installErr = ""
	u.mu.Unlock()

	err := u.replaceBundle(tag)
	if err == nil {
		if err = relaunch(u.bundle); err != nil {
			err = fmt.Errorf("%s is installed, but restarting failed (%v) — quit Marionette and open it again", tag, err)
		}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if err != nil {
		u.state = ""
		u.installErr = err.Error()
		return
	}
	u.state = "restarting"
	quit := exit
	go func() {
		time.Sleep(500 * time.Millisecond) // let the window show it
		quit(0)
	}()
}

// replaceBundle swaps the running .app for release tag. The new bundle is
// unpacked next to the old one, so the swap is two renames on one volume,
// and checked to run and report tag before anything is replaced.
func (u *updater) replaceBundle(tag string) error {
	staging, err := os.MkdirTemp(filepath.Dir(u.bundle), ".marionette-update-")
	if err != nil {
		return fmt.Errorf("cannot write next to %s (%w) — download the update instead", u.bundle, err)
	}
	defer os.RemoveAll(staging)

	archive := filepath.Join(staging, "Marionette-macos.zip")
	if err := u.download(u.base+"/download/"+tag+"/Marionette-macos.zip", archive); err != nil {
		return err
	}
	if out, err := command("ditto", "-xk", archive, staging).CombinedOutput(); err != nil {
		return fmt.Errorf("unpacking the update: %v: %s", err, out)
	}
	app := filepath.Join(staging, "Marionette.app")
	out, err := command(filepath.Join(app, "Contents", "MacOS", "marionette"), "--version").Output()
	if got := strings.TrimSpace(string(out)); err != nil || got != tag {
		return fmt.Errorf("the downloaded app does not run as %s (got %q, %v)", tag, got, err)
	}

	old := filepath.Join(staging, "old.app")
	if err := os.Rename(u.bundle, old); err != nil {
		return replaceError(err)
	}
	if err := os.Rename(app, u.bundle); err != nil {
		os.Rename(old, u.bundle)
		return replaceError(err)
	}
	return nil
}

func replaceError(err error) error {
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("macOS did not let Marionette replace itself (%v) — allow it under System Settings → Privacy & Security → App Management, or download the update instead", err)
	}
	return fmt.Errorf("replacing the app: %w", err)
}

func (u *updater) download(src, dest string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", src, nil)
	if err != nil {
		return err
	}
	res, err := u.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("downloading the update: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading the update: %s", res.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, res.Body); err != nil {
		f.Close()
		return fmt.Errorf("downloading the update: %w", err)
	}
	return f.Close()
}

func (u *updater) canInstallLocked() bool {
	return goos == "darwin" && u.bundle != ""
}

// view is the update state the window shows.
func (u *updater) view() map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	_, release := parseVersion(u.current)
	v := map[string]any{
		"enabled": release, "current": u.current, "latest": u.latest,
		"available": newer(u.latest, u.current), "can_install": u.canInstallLocked(),
		"state": u.state, "checks": u.checks, "check_error": u.checkErr, "install_error": u.installErr,
	}
	if u.latest != "" {
		v["url"] = u.base + "/tag/" + u.latest
	}
	return v
}

func (u *updater) httpClient() *http.Client {
	if u.client != nil {
		c := *u.client
		return &c
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = proxyFor
	return &http.Client{Transport: t}
}

// proxyFor honours the proxy environment variables, and on macOS the system
// proxy, which an app opened from Finder never sees in its environment.
func proxyFor(r *http.Request) (*url.URL, error) {
	if p, err := http.ProxyFromEnvironment(r); p != nil || err != nil {
		return p, err
	}
	if goos != "darwin" {
		return nil, nil
	}
	out, err := command("scutil", "--proxy").Output()
	if err != nil {
		return nil, nil
	}
	return scutilProxy(string(out)), nil
}

// scutilProxy reads the HTTPS or SOCKS proxy from `scutil --proxy` output.
func scutilProxy(out string) *url.URL {
	kv := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(line, " : "); ok {
			kv[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	for _, p := range []struct{ key, scheme string }{{"HTTPS", "http"}, {"SOCKS", "socks5"}} {
		host := kv[p.key+"Proxy"]
		if kv[p.key+"Enable"] != "1" || host == "" {
			continue
		}
		if port := kv[p.key+"Port"]; port != "" {
			host = net.JoinHostPort(host, port)
		}
		return &url.URL{Scheme: p.scheme, Host: host}
	}
	return nil
}

// appBundle is the .app that executable exe belongs to, or "" when it is not
// in one, or runs from the read-only copy macOS makes of an app opened from
// where it was downloaded (App Translocation).
func appBundle(exe string) string {
	macos := filepath.Dir(exe)
	contents := filepath.Dir(macos)
	app := filepath.Dir(contents)
	if filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" || filepath.Ext(app) != ".app" ||
		strings.Contains(app, "/AppTranslocation/") {
		return ""
	}
	return app
}

// relaunch starts a second copy of the app, which waits for this one to
// free the port, and exit ends this one. Tests replace both.
var (
	relaunch = func(bundle string) error {
		return command("open", "-n", bundle, "--args", "--relaunch").Run()
	}
	exit = os.Exit
)

type semver struct {
	n   [3]int
	pre bool // anything after the numbers: a pre-release, or a build past the tag
}

// parseVersion reads "v1.2.3", optionally followed by "-…" as in
// `git describe` output ("v1.2.3-4-gabcdef-dirty").
func parseVersion(s string) (semver, bool) {
	var v semver
	s, ok := strings.CutPrefix(s, "v")
	if !ok {
		return v, false
	}
	s, rest, _ := strings.Cut(s, "-")
	v.pre = rest != ""
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v.n[i] = n
	}
	return v, true
}

// newer reports whether release tag latest is newer than version current.
// A build past a tag counts as that tag: the next release is still newer.
func newer(latest, current string) bool {
	l, ok1 := parseVersion(latest)
	c, ok2 := parseVersion(current)
	if !ok1 || !ok2 || l.pre {
		return false
	}
	for i := range l.n {
		if l.n[i] != c.n[i] {
			return l.n[i] > c.n[i]
		}
	}
	return false
}
