//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// command runs a helper program without flashing a console window, which a
// GUI-subsystem app would otherwise get for every tasklist/taskkill call.
func command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return cmd
}
