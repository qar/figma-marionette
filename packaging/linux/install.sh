#!/usr/bin/env sh
# Install Marionette for the current user: the binary into ~/.local/bin and a
# launcher into the applications menu. Needs WebKitGTK 4.1 at runtime:
#   Debian/Ubuntu: sudo apt install libwebkit2gtk-4.1-0
#   Fedora:        sudo dnf install webkit2gtk4.1
#   Arch:          sudo pacman -S webkit2gtk-4.1
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
BIN="$HOME/.local/bin"
APPS="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
ICONS="${XDG_DATA_HOME:-$HOME/.local/share}/icons/hicolor/256x256/apps"

mkdir -p "$BIN" "$APPS" "$ICONS"
install -m 755 "$HERE/marionette" "$BIN/marionette"
install -m 644 "$HERE/marionette.png" "$ICONS/marionette.png"
sed "s|^Exec=.*|Exec=$BIN/marionette|" "$HERE/marionette.desktop" > "$APPS/marionette.desktop"
command -v update-desktop-database >/dev/null && update-desktop-database "$APPS" || true

echo "Installed. Open Marionette from your applications menu, or run: $BIN/marionette"
