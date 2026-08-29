---
name: figma-bridge
description: Read and write Figma files with unlimited local script execution, via the self-hosted figma-agent-bridge plugin. Use instead of the official Figma MCP write tools (use_figma / get_screenshot) whenever creating, editing, inspecting, or screenshotting Figma designs — the official MCP is capped at 20 calls/month on the Starter plan.
---

# Figma Bridge

Executes arbitrary Plugin API scripts against the Figma file the user currently has open. Free, unmetered, and it round-trips screenshots — use it for **all** Figma canvas work.

Repo: `/Users/qiaoanran/projects/project-e/figma-mm-builder` (github.com/qar/figma-agent-bridge, work on `dev`).

## Run a script

```bash
cat > /tmp/my-script.js <<'EOF'
return { pages: figma.root.children.map(p => p.name) };
EOF
node /Users/qiaoanran/projects/project-e/figma-mm-builder/run.mjs /tmp/my-script.js --timeout 90
```

`run.mjs` starts the bridge server itself if it is not running — never ask the user to start it. The result JSON prints to stdout.

**The one thing only the user can do**: keep the *MM Figma Builder* plugin open in Figma desktop (Plugins → Development → MM Figma Builder), panel showing "Connected · idle". If a run ends in `timeout after Ns — is the plugin open in Figma?`, ask them to (re)open it. The plugin polls every 500ms, so it reconnects on its own once running.

## Script contract

Same shape as the official MCP `use_figma`: plain JS, top-level `await`, `return` a JSON-serializable value. But this is the **raw Plugin API** — the MCP-only conveniences (`figma.createAutoLayout`, `node.query`, `node.set`, `node.screenshot`) do **not** exist. A `helpers` object fills the gap:

| Helper | Use |
|---|---|
| `helpers.hex("#RRGGBB")` | → `{r,g,b}` in 0–1 range |
| `helpers.S("#RRGGBB", opacity?)` | → SOLID fills array |
| `helpers.AL(dir, opts)` | auto-layout frame; opts: `name, itemSpacing, padding*, cornerRadius, fill, center` |
| `helpers.T(chars, opts)` | text node; opts: `family, style, size, color, ls`. **Load the font first.** |
| `helpers.SVG(markup, w, h)` | vector from SVG markup |
| `await helpers.shot(node, scale?)` | screenshot → `run.mjs` writes a PNG and returns its path; then `Read` that path |

## Rules that bite

1. **Load every font before creating or mutating text** — `await figma.loadFontAsync({family, style})`. Chinese text needs `Noto Sans SC`; Korean needs `Apple SD Gothic Neo`; Cyrillic and Latin/digits are fine in `Inter`. A missing glyph renders as blank space, silently.
2. **`figma.skipInvisibleInstanceChildren = false`** at the top of any script that must reach hidden nodes inside instances (e.g. a hidden checkmark you want to show). Otherwise `findOne` returns null.
3. **`layoutSizing*` = `'FILL'` only after `appendChild`**, and only inside an auto-layout parent.
4. **Wrapping text**: set `textAutoResize = "HEIGHT"` then `layoutSizingHorizontal = "FILL"`, or it collapses to a sliver.
5. **Errors are atomic** — a failed script changes nothing. Read the message, fix, rerun.
6. Return **node IDs**, never node objects (must be JSON-serializable).
7. **Verify visually.** After building, `helpers.shot` the node and `Read` the PNG. Structural success is not visual success.

## Reading the user's selection

```js
return figma.currentPage.selection.map(n => ({ id: n.id, name: n.name, type: n.type }));
```

Useful when the user says "change this" — read the selection instead of guessing.

## Still use the official Figma MCP for

`create_new_file` and `whoami` (both exempt from the rate limit), plus the cloud-only features this bridge cannot reach: `search_design_system`, `get_libraries`, Code Connect. Everything that touches the canvas goes through the bridge.
