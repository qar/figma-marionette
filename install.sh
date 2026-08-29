#!/usr/bin/env bash
# Install the figma-bridge skill for users who are not using the Claude Code
# plugin system. Copies the skill into ~/.claude/skills/ and bakes this
# checkout's absolute path into it, so `node "$BRIDGE/run.mjs"` resolves
# without CLAUDE_PLUGIN_ROOT being set.
#
#   ./install.sh            # install to ~/.claude/skills/figma-bridge
#   ./install.sh --project  # install to ./.claude/skills/figma-bridge instead
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC="$HERE/skills/figma-bridge/SKILL.md"

if [ "${1:-}" = "--project" ]; then
  DEST_DIR="$PWD/.claude/skills/figma-bridge"
else
  DEST_DIR="$HOME/.claude/skills/figma-bridge"
fi

[ -f "$SRC" ] || { echo "error: $SRC not found" >&2; exit 1; }

mkdir -p "$DEST_DIR"
sed "s|__BRIDGE_HOME__|$HERE|g" "$SRC" > "$DEST_DIR/SKILL.md"

echo "installed skill  -> $DEST_DIR/SKILL.md"
echo "bridge home      -> $HERE"
echo
echo "Next: in Figma desktop, Plugins → Development → Import plugin from manifest…"
echo "      and choose $HERE/manifest.json (first time only)."
