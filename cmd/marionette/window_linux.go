//go:build linux && cgo

package main

/*
#cgo pkg-config: gtk+-3.0
#include <gtk/gtk.h>
#include <signal.h>

static void prepare_window(void *window) {
	// The "marionette" theme icon comes from the Linux package's install.sh.
	gtk_window_set_icon_name(GTK_WINDOW(window), "marionette");

	// WebKitGTK's JavaScriptCore installs signal handlers without SA_ONSTACK.
	// If one of those signals lands on a Go thread, the Go runtime aborts
	// ("non-Go code set up signal handler without SA_ONSTACK flag").
	for (int sig = 1; sig < 32; sig++) {
		struct sigaction sa;
		if (sigaction(sig, NULL, &sa) != 0) continue;
		if (sa.sa_handler == SIG_DFL || sa.sa_handler == SIG_IGN) continue;
		if (sa.sa_flags & SA_ONSTACK) continue;
		sa.sa_flags |= SA_ONSTACK;
		sigaction(sig, &sa, NULL);
	}
}
*/
import "C"

import "unsafe"

func prepareWindow(window unsafe.Pointer) {
	C.prepare_window(window)
}
