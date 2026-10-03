//go:build darwin && cgo

package main

import "unsafe"

// Nothing to do: the .app bundle's AppIcon.icns supplies the window icon.
func prepareWindow(unsafe.Pointer) {}
