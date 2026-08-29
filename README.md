# Figma Agent Bridge

Unmetered read/write access to Figma from an AI agent, through the **Figma Plugin API** — a self-hosted replacement for the write path of the official Figma MCP server, which allows only 20 tool calls per month on the Starter plan.

A local server queues scripts; a Figma plugin polls it and executes them against the file you have open, returning JSON results and screenshots.

## Install

### As a Claude Code plugin (recommended)

```
/plugin marketplace add qar/figma-agent-bridge
/plugin install figma-agent-bridge
```

This installs the `figma-bridge` skill, so Claude reaches for the bridge automatically whenever a task touches Figma. Then import the Figma-side plugin once (see below).

### Manually (any agent, or Claude Code without the plugin system)

```bash
git clone https://github.com/qar/figma-agent-bridge.git
cd figma-agent-bridge
./install.sh              # → ~/.claude/skills/figma-bridge
./install.sh --project    # → ./.claude/skills/figma-bridge
```

`install.sh` bakes the checkout's absolute path into the installed skill, so the agent can find `run.mjs` without any environment variable.

### Figma side (required either way, once)

In **Figma desktop**: Plugins → Development → Import plugin from manifest… → pick this repo's `manifest.json`. Run **Figma Agent Bridge** once per working session and leave the panel open; it should read "Connected · idle".

Installed via the Claude plugin system, the repo lives under `~/.claude/plugins/cache/` — ask Claude for the exact `manifest.json` path.

## Architecture

```
agent / CLI                    bridge-server.mjs               Figma desktop plugin
  node run.mjs xx.js  ── POST /run ──▶  queue  ◀── GET /pull (500ms poll) ── ui.html
  poll GET /result    ◀── store  ◀──────────── POST /result ──────────── code.js evals script
```

- `bridge-server.mjs` — zero-dependency Node server, bound to 127.0.0.1:3055 only
- `code.js` + `ui.html` — the plugin: the UI polls the bridge, the main thread executes received scripts via AsyncFunction
- `run.mjs` — CLI: submits a script file and waits for the result; auto-starts the server; saves any returned PNG to disk
- `skills/figma-bridge/SKILL.md` — the agent-facing usage guide
- `scripts/` — example scripts

## Usage

```bash
node run.mjs my-script.js            # default 120s timeout
node run.mjs my-script.js --timeout 300
node run.mjs my-script.js --no-spawn # fail instead of auto-starting the server
```

`run.mjs` starts `bridge-server.mjs` detached if nothing is listening, so there is no separate start step. Logs from an auto-started instance go to `/tmp/figma-bridge-server.log`. Override the port with `FIGMA_BRIDGE_PORT`.

## Script contract

Plain JavaScript; top-level `await` allowed; `return` a JSON-serializable value. The full Plugin API is available through the `figma` global, plus an injected `helpers` object:

| Helper | Purpose |
|---|---|
| `helpers.hex("#RRGGBB")` | Figma color object (0–1 range) |
| `helpers.S("#RRGGBB", opacity?)` | SOLID fills array |
| `helpers.AL(dir, opts)` | frame with auto-layout preconfigured (the vanilla API has no `createAutoLayout`) |
| `helpers.T(chars, opts)` | text node — load the font with `loadFontAsync` first |
| `helpers.SVG(markup, w, h)` | vector node from SVG markup |
| `await helpers.shot(node, scale?)` | node screenshot; `run.mjs` saves it and returns the PNG path |

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

- The plugin must stay open in Figma desktop; it operates on the **currently open file**
- Cloud-side features of the official MCP are out of scope: `search_design_system`, `get_libraries`, Code Connect, `create_new_file` (the last of those is exempt from the MCP rate limit anyway)
- Results must be JSON-serializable — return node IDs, not node objects

## Roadmap

- [ ] TypeScript + `@figma/plugin-typings` build chain
- [ ] WebSocket instead of polling; multi-file / multi-job routing
- [ ] Before Figma Community publishing: add a `networkAccess` declaration and an auth token

## License

MIT
