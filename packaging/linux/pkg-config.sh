#!/bin/sh
# pkg-config shim for building the Linux window. webview_go asks for
# webkit2gtk-4.0, which current distros (Ubuntu 24.04+, Fedora 40+) no longer
# ship; 4.1 differs only in its libsoup version, which webview never touches.
for arg; do
  shift
  [ "$arg" = webkit2gtk-4.0 ] && arg=webkit2gtk-4.1
  set -- "$@" "$arg"
done
exec pkg-config "$@"
