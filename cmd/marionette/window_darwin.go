//go:build darwin && cgo

package main

import webview "github.com/webview/webview_go"

func runWindow(url string, a *actions) {
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("Marionette")
	w.SetSize(460, 720, webview.HintNone)
	w.Bind("revealPlugin", a.revealPlugin)
	w.Bind("installSkill", a.installSkill)
	w.Bind("openURL", a.openURL)
	w.Bind("copyText", a.copyText)
	w.Navigate(url)
	w.Run()
}

func showError(msg string) {
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("Marionette")
	w.SetSize(420, 200, webview.HintFixed)
	w.SetHtml(errorPage(msg))
	w.Run()
}
