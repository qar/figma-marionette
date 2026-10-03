// Package marionette holds the files the desktop app ships inside its binary:
// the Figma plugin it extracts for import, and the Claude Code skill it can
// install. They live at the repo root so the Claude Code plugin and the app
// share one copy.
package marionette

import "embed"

//go:embed figma-plugin/manifest.json figma-plugin/code.js figma-plugin/ui.html
var FigmaPlugin embed.FS

//go:embed skills/figma-marionette/SKILL.md
var Skill []byte
