//go:build windows

package main

import (
	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

const webView2Download = "https://go.microsoft.com/fwlink/p/?LinkId=2124703"

func newWindow(width, height uint) webview2.WebView {
	return webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title: "Marionette", Width: width, Height: height, Center: true,
		},
	})
}

func runWindow(url string, a *actions) {
	w := newWindow(460, 720)
	if w == nil {
		// Without a window this GUI-subsystem process would be invisible and
		// unquittable, so explain and exit instead.
		if messageBox("Marionette needs the Microsoft Edge WebView2 Runtime, which is missing on this PC.\n\nClick OK to open its download page, then start Marionette again.", windows.MB_OKCANCEL|windows.MB_ICONWARNING) == 1 {
			openExternal(webView2Download)
		}
		return
	}
	defer w.Destroy()
	w.Bind("revealPlugin", a.revealPlugin)
	w.Bind("installSkill", a.installSkill)
	w.Bind("openURL", a.openURL)
	w.Bind("copyText", a.copyText)
	w.Bind("addToFigma", a.addToFigma)
	w.Bind("figmaProgress", a.figmaProgress)
	w.Bind("checkUpdates", a.checkUpdates)
	w.Bind("installUpdate", a.installUpdate)
	w.Navigate(url)
	w.Run()
}

func showError(msg string) {
	w := newWindow(420, 200)
	if w == nil {
		messageBox(msg, windows.MB_OK|windows.MB_ICONERROR)
		return
	}
	defer w.Destroy()
	w.SetHtml(errorPage(msg))
	w.Run()
}

func messageBox(text string, style uint32) int32 {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString("Marionette")
	ret, _ := windows.MessageBox(0, t, c, style)
	return ret
}
