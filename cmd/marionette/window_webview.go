//go:build (darwin || linux) && cgo

package main

import (
	"log"
	"os"

	webview "github.com/webview/webview_go"
)

// hasDisplay reports whether a native window can open. On Linux a session
// without X11 or Wayland (SSH, a server) runs headless instead.
func hasDisplay() bool {
	return goos != "linux" || os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}

func runWindow(url string, a *actions) {
	if !hasDisplay() {
		log.Printf("no display; running headless — status page: %s (Ctrl+C to stop)", url)
		waitForInterrupt()
		return
	}
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("Marionette")
	w.SetSize(460, 720, webview.HintNone)
	prepareWindow(w.Window())
	w.Bind("revealPlugin", a.revealPlugin)
	w.Bind("installSkill", a.installSkill)
	w.Bind("openURL", a.openURL)
	w.Bind("copyText", a.copyText)
	w.Navigate(url)
	w.Run()
}

func showError(msg string) {
	if !hasDisplay() {
		log.Fatal(msg)
	}
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("Marionette")
	w.SetSize(420, 200, webview.HintFixed)
	prepareWindow(w.Window())
	w.SetHtml(errorPage(msg))
	w.Run()
}
