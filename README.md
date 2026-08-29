# MM Figma Builder

A local bridge plugin that lets an AI agent (or any CLI) read and write Figma files through the **Figma Plugin API** — free and without call quotas — replacing the write path of the official Figma MCP server (which allows only 20 tool calls per month on the Starter plan).

## Architecture

```
Claude Code / CLI                bridge-server.mjs               Figma desktop plugin
  node run.mjs xx.js  ── POST /run ──▶  queue  ◀── GET /pull (500ms poll) ── ui.html
  poll GET /result    ◀── store  ◀──────────── POST /result ──────────── code.js evals script
```

- `bridge-server.mjs` — zero-dependency Node server, bound to 127.0.0.1:3055 only
- `code.js` + `ui.html` — the plugin: the UI polls the bridge, the main thread executes received scripts via AsyncFunction
- `run.mjs` — CLI: submits a script file and waits for the result; any `{$png: base64}` value in the result is saved as a PNG under /tmp and replaced by its file path
- `scripts/` — historical / example scripts (`mm-test-oneshot.js` was the original standalone one-shot build, kept for reference)

## Usage

```bash
# 1. Start the bridge server (keep it running)
node bridge-server.mjs

# 2. Figma desktop: Plugins → Development → Import plugin from manifest… (first time only)
#    Then run "MM Figma Builder" once per working session; the panel should
#    show "Connected · idle".
#    ⚠️ Keep the plugin panel open — closing it disconnects the bridge.

# 3. Submit scripts
node run.mjs my-script.js            # default 120s timeout
node run.mjs my-script.js --timeout 300
```

## Script contract (same conventions as the official MCP `use_figma`)

- Plain JavaScript; top-level `await` allowed; `return` a JSON-serializable value
- The full Plugin API is available through the `figma` global
- A `helpers` object is injected:
  - `helpers.hex("#RRGGBB")` → Figma color object (0–1 range)
  - `helpers.S("#RRGGBB", opacity?)` → SOLID fills array
  - `helpers.AL(dir, opts)` → frame with auto-layout preconfigured (the vanilla API has no `createAutoLayout`)
  - `helpers.T(chars, opts)` → text node (**load the font with `loadFontAsync` first**)
  - `helpers.SVG(markup, w, h)` → vector node from SVG markup
  - `await helpers.shot(node, scale?)` → node screenshot; `run.mjs` saves it to disk and returns the PNG path

Example:

```js
await figma.loadFontAsync({ family: "Inter", style: "Bold" });
const { AL, T, shot } = helpers;
const card = AL("VERTICAL", { itemSpacing: 8, paddingLeft: 16, paddingRight: 16, paddingTop: 16, paddingBottom: 16, cornerRadius: 16, fill: "#FFFFFF" });
card.appendChild(T("Hello", { family: "Inter", style: "Bold", size: 24 }));
figma.currentPage.appendChild(card);
return { id: card.id, preview: await shot(card, 2) };
```

## Security

Scripts are eval'ed inside the Figma plugin sandbox, and the bridge server listens on 127.0.0.1 with no authentication. **Never** expose the port beyond localhost, and only submit scripts from sessions you trust.

## Known limitations

- The plugin must stay open in Figma desktop for scripts to run; it operates on the **currently open file**
- Cloud-side features of the official MCP are out of scope: `search_design_system` (cross-library search), Code Connect mappings, and `create_new_file` — keep using the official MCP for those (`create_new_file` and `whoami` are exempt from its rate limit)
- Results must be JSON-serializable (return node IDs, not node objects)

## Roadmap

- [ ] TypeScript + `@figma/plugin-typings` build chain
- [ ] WebSocket instead of polling; multi-file / multi-job routing
- [ ] Before Community publishing: add a `networkAccess` declaration and an auth token
