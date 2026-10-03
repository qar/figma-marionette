---
name: figma-marionette
description: Read and write Figma files with unlimited local script execution, via the Marionette desktop app and its Figma plugin. Use instead of the official Figma MCP write tools (use_figma / get_screenshot) whenever creating, editing, inspecting, or screenshotting Figma designs — the official MCP is capped at 20 calls/month on the Starter plan.
---

# Marionette

Executes arbitrary Plugin API scripts against the Figma file the user currently has open, through the Marionette desktop app (a local server on `127.0.0.1:3055`) and its Figma plugin. Free, unmetered, and it round-trips screenshots — use it for **all** Figma canvas work.

## Run a script

```bash
cat > /tmp/my-script.js <<'EOF'
return { pages: figma.root.children.map(p => p.name) };
EOF
curl -sS --noproxy '*' --data-binary @/tmp/my-script.js 'http://127.0.0.1:3055/run?wait=90'
```

The response is JSON:

| HTTP | Body | Meaning |
|---|---|---|
| 200 | `{"ok": true, "result": …, "ms": …}` | The script's return value. Screenshots arrive as PNG file paths — `Read` them. |
| 422 | `{"ok": false, "error", "stack"}` | The script threw. Nothing in the file changed. |
| 504 | `"state": "expired"` | The Figma plugin never picked the script up; it was discarded. See below. |
| 504 | `"state": "running"` | Still executing. Keep waiting: `curl -sS --noproxy '*' 'http://127.0.0.1:3055/result?id=<id>&wait=90'` — it returns the outcome, or `{"pending": true}` if still running. |

Requests to localhost may need the command sandbox disabled.

## When it is not reachable

- **Connection refused** → the Marionette app is not running. On macOS run `open -a Marionette`, wait ~2s, retry. If that fails because the app is not installed, tell the user and offer to install it (macOS):

  ```bash
  curl -fsSL -o /tmp/Marionette.zip https://github.com/qar/figma-marionette/releases/latest/download/Marionette-macos.zip && ditto -xk /tmp/Marionette.zip /Applications && open -a Marionette
  ```

  On Windows, point the user to https://marionette.otimififi.site.
- **504 with `"state": "expired"`** → the app is up but the Figma plugin is not connected. Ask the user to run it in Figma desktop: *Plugins → Development → Marionette*, and leave its panel open. First time only: in the Marionette window's **Setup** section, click **Add to Figma** (it restarts Figma once).
- Check any time: `curl -sS --noproxy '*' http://127.0.0.1:3055/health` → `plugin_connected`.

Never ask the user to start a server or run a terminal command — the app window is the whole setup.

## Script contract

Same shape as the official MCP `use_figma`: plain JS, top-level `await`, `return` a JSON-serializable value. But this is the **raw Plugin API** — the MCP-only conveniences (`figma.createAutoLayout`, `node.query`, `node.set`, `node.screenshot`) do **not** exist. A `helpers` object fills the gap:

| Helper | Use |
|---|---|
| `helpers.hex("#RRGGBB")` | → `{r,g,b}` in 0–1 range |
| `helpers.S("#RRGGBB", opacity?)` | → SOLID fills array |
| `helpers.AL(dir, opts)` | auto-layout frame; opts: `name, itemSpacing, padding*, cornerRadius, fill, center` |
| `helpers.T(chars, opts)` | text node; opts: `family, style, size, color, ls`. **Load the font first.** |
| `helpers.SVG(markup, w, h)` | vector from SVG markup |
| `await helpers.shot(node, scale?)` | screenshot → comes back as a PNG file path; then `Read` that path |

Start a script with a `// comment` saying what it does — the Marionette window lists scripts by their first line.

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

`create_new_file` and `whoami` (both exempt from the rate limit), plus the cloud-only features Marionette cannot reach: `search_design_system`, `get_libraries`, Code Connect. Everything that touches the canvas goes through Marionette.
