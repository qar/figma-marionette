//go:build !windows && !((darwin || linux) && cgo)

package main

import "log"

// Without a native webview (a cgo-less build, e.g. the headless Linux one) the
// status page opens in the default browser and the bridge runs until interrupted.
func runWindow(url string, a *actions) {
	log.Printf("status page: %s (Ctrl+C to stop)", url)
	openExternal(url)
	waitForInterrupt()
}

func showError(msg string) {
	log.Fatal(msg)
}
