// Command marionette is the Marionette desktop app: it runs the local bridge
// between AI agents and the Marionette Figma plugin, and shows a small status
// window. Closing the window stops the bridge.
//
//	marionette              # window (macOS, Windows); browser tab elsewhere
//	marionette --headless   # no window, serve until interrupted
//
// MARIONETTE_PORT overrides the default port 3055.
package main

import (
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

var (
	version = "dev" // set by -ldflags "-X main.version=..."
	goos    = runtime.GOOS
)

func main() {
	headless := flag.Bool("headless", false, "run without a window until interrupted")
	showVersion := flag.Bool("version", false, "print the version and exit")
	relaunched := flag.Bool("relaunch", false, "started by an update: wait for the old copy to free the port")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	// A window-only build has no console, so startup failures must be shown.
	fail := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		log.Print(msg)
		if !*headless {
			showError(msg)
		}
		os.Exit(1)
	}

	port := 3055
	if s := os.Getenv("MARIONETTE_PORT"); s != "" {
		p, err := strconv.Atoi(s)
		if err != nil || p <= 0 || p > 65535 {
			fail("Invalid MARIONETTE_PORT %q.", s)
		}
		port = p
	}

	dataDir, err := appDataDir()
	if err != nil {
		fail("Cannot create the Marionette data folder: %v", err)
	}
	if logFile, err := os.OpenFile(filepath.Join(dataDir, "marionette.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		log.SetOutput(io.MultiWriter(os.Stderr, logFile))
	}

	token, err := loadToken(dataDir)
	if err != nil {
		fail("%v", err)
	}
	b := newBridge(port, token, filepath.Join(os.TempDir(), "marionette"))
	if b.pluginDir, err = extractPlugin(dataDir, port, token); err != nil {
		fail("Cannot write the Figma plugin files: %v", err)
	}
	b.skillPath = skillPath()
	if err := refreshSkill(b.skillPath); err != nil {
		log.Printf("updating the installed skill: %v", err)
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	for wait := 10 * time.Second; err != nil && *relaunched && wait > 0; wait -= 200 * time.Millisecond {
		time.Sleep(200 * time.Millisecond)
		ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	}
	if err != nil {
		fail("Port %d is already in use — Marionette is probably already running. Close the other window, or set MARIONETTE_PORT to a free port.", port)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	go func() { log.Fatal(http.Serve(ln, b.handler())) }()
	log.Printf("marionette %s listening on %s", version, url)
	log.Printf("figma plugin manifest: %s", filepath.Join(b.pluginDir, "manifest.json"))

	if *headless {
		waitForInterrupt()
		return
	}
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	b.updates = newUpdater(version, appBundle(exe))
	go b.updates.run()
	runWindow(url, &actions{pluginDir: b.pluginDir, updates: b.updates})
}

func errorPage(msg string) string {
	return `<!doctype html><meta charset="utf-8"><meta name="color-scheme" content="light dark">` +
		`<body style="font:14px/1.5 -apple-system,'Segoe UI',sans-serif;margin:24px">` +
		`<b>Marionette can't start</b><p>` + html.EscapeString(msg) + `</p></body>`
}

func waitForInterrupt() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
}
