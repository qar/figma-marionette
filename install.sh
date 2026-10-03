#!/usr/bin/env bash
# Install the figma-marionette skill for agents that don't use the Claude Code
# plugin system. The Marionette app's "Install skill" button does the same.
#
#   ./install.sh            # install to ~/.claude/skills/figma-marionette
#   ./install.sh --project  # install to ./.claude/skills/figma-marionette instead
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC="$HERE/skills/figma-marionette/SKILL.md"

if [ "${1:-}" = "--project" ]; then
  DEST_DIR="$PWD/.claude/skills/figma-marionette"
else
  DEST_DIR="$HOME/.claude/skills/figma-marionette"
fi

[ -f "$SRC" ] || { echo "error: $SRC not found" >&2; exit 1; }

mkdir -p "$DEST_DIR"
cp "$SRC" "$DEST_DIR/SKILL.md"

echo "installed skill -> $DEST_DIR/SKILL.md"
echo
echo "Next: install and open the Marionette app — https://marionette.otimififi.site"
