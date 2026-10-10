# Marionette

**Pull Figma's strings from your AI agent.** · [marionette.otimififi.site](https://marionette.otimififi.site)

Unmetered read/write access to Figma from an AI agent, through the **Figma Plugin API** — a self-hosted replacement for the write path of the official Figma MCP server, which allows only 20 tool calls per month on the Starter plan.

Marionette is a small desktop app. Open it, run its Figma plugin once, and any agent on your machine can execute scripts against the Figma files you have open — creating, editing, inspecting and screenshotting — with nothing to start or configure in a terminal.

## How it works

```
AI agent                         Marionette app                  Figma desktop
  curl POST /run?wait=90  ──▶   127.0.0.1:3055   ◀── GET /pull (every 500ms) ──  Marionette plugin
  ◀── result JSON + PNG paths    queue + window   ◀── POST /result ─────────────  runs the script
```

- **App** (`cmd/marionette`) — a ~6 MB Go binary: the local bridge server plus a native status window (WKWebView on macOS, WebView2 on Windows) showing whether the Figma plugin is connected and what scripts ran. Closing the window stops it.
- **Figma plugin** (`figma-plugin/`) — embedded in the app; polls the bridge and executes scripts with the Plugin API.
- **Skill** (`skills/figma-marionette/SKILL.md`) — teaches Claude Code to reach for Marionette whenever a task touches Figma.

## Install

### 1. The app

| Platform | Download |
|---|---|
| macOS 11+ (Apple Silicon & Intel) | [Marionette-macos.zip](https://github.com/qar/figma-marionette/releases/latest/download/Marionette-macos.zip) |
| Windows 10/11 (experimental) | [Marionette-windows-amd64.zip](https://github.com/qar/figma-marionette/releases/latest/download/Marionette-windows-amd64.zip) |

The macOS app is not notarized yet. After unzipping, macOS will refuse the first launch: open **System Settings → Privacy & Security** and click **Open Anyway**. Or install from Terminal, which skips the quarantine prompt:

```bash
curl -fsSL -o /tmp/Marionette.zip https://github.com/qar/figma-marionette/releases/latest/download/Marionette-macos.zip \
  && ditto -xk /tmp/Marionette.zip /Applications && open -a Marionette
```

#### Updates

Marionette looks for a newer release on GitHub when it starts and once a day, and the window offers it when there is one (or click **Check for updates** next to the version). On macOS, **Update and restart** downloads the release, checks that it runs, swaps it in place of the app and restarts — no Gatekeeper prompt, since the app's own download is not quarantined. If macOS refuses to let it replace itself, the window says how to allow it (System Settings → Privacy & Security → App Management); on Windows it links to the download. Updating also refreshes the skill if the window installed it. Requests use the proxy environment variables, or else the macOS system proxy.

### 2. The Figma plugin (once)

In the Marionette window, open **Setup** and click **Add to Figma**. Marionette adds its plugin to Figma desktop's list of development plugins; if Figma is open it offers to restart it, since Figma only reads that list at startup. (It edits Figma's `settings.json` — an undocumented format — after backing it up next to the original.)

If that doesn't work, use **Add it manually instead**: in Figma desktop, Plugins → Development → Import plugin from manifest…, then in the file picker press ⌘⇧G (macOS) or paste into the File name box (Windows) the path the window shows.

From then on, run **Plugins → Development → Marionette** once per Figma session and leave its panel open. The app window turns green when it connects. Run it in each file you want your agent to reach; with several open, the window lists them, and agents have to say which one each script is for.

### 3. Your agent

**Claude Code** — either click **Install skill** in the Marionette window, or install the plugin:

```
/plugin marketplace add qar/figma-marionette
/plugin install figma-marionette
```

**Any other agent** — call the HTTP API below. `./install.sh` copies the skill into `~/.claude/skills/` (or `./.claude/skills/` with `--project`) for agents that read that layout.

## HTTP API

All endpoints listen on `127.0.0.1:3055` (override with `MARIONETTE_PORT`).

```bash
curl -sS --data-binary @script.js 'http://127.0.0.1:3055/run?wait=90'
```

| Request | Response |
|---|---|
| `POST /run?wait=N&file=F` — body: the script (or `{"code": "..."}` JSON) | `200 {ok: true, result, ms, file}` · `422 {ok: false, error, stack, file}` when the script throws · `409 {state: "choose_file", files}` when it is unclear which file to run in (nothing ran) · `504 {state: "expired"}` when the plugin never picked it up (the script is discarded) · `504 {state: "running"}` when it is still executing |
| `POST /run` (no `wait`) | `{id, file}` |
| `GET /result?id=…&wait=N` | the outcome as above, or `200 {pending: true, state}` if the job hasn't finished within `wait` (polling never discards a job) |
| `GET /health` | `{ok, version, plugin_connected, files, queued}` |

### Choosing the file

Each Figma file running the plugin is listed in `files` as `{id, name, key, page}`: `id` is random per plugin run, `key` is the file key from its `figma.com/design/<key>/…` URL (present when Figma exposes it to the plugin), `name` the file name and `page` the page open in it. `file=` accepts any of `id`, `key` or `name`.

- Without `file`, a script runs in the only open file. With several open it is refused with `409`, so it never lands in a guessed file; with none open it waits for the first file to connect.
- With `file`, it runs in the one open file that matches, or is refused with `409` if none or several match.
- Outcomes carry `file: {id, name}` — the file the script ran in.

Any `{$png: base64}` value in a result — what `helpers.shot()` returns — is written to a PNG file under the system temp directory and replaced with its path.

## Script contract

Plain JavaScript; top-level `await` allowed; `return` a JSON-serializable value. The full Plugin API is available through the `figma` global, plus an injected `helpers` object:

| Helper | Purpose |
|---|---|
| `helpers.hex("#RRGGBB")` | Figma color object (0–1 range) |
| `helpers.S("#RRGGBB", opacity?)` | SOLID fills array |
| `helpers.AL(dir, opts)` | frame with auto-layout preconfigured (the vanilla API has no `createAutoLayout`) |
| `helpers.T(chars, opts)` | text node — load the font with `loadFontAsync` first |
| `helpers.SVG(markup, w, h)` | vector node from SVG markup |
| `await helpers.shot(node, scale?)` | node screenshot, returned as a PNG file path |

```js
// Hello card
await figma.loadFontAsync({ family: "Inter", style: "Bold" });
const { AL, T, shot } = helpers;
const card = AL("VERTICAL", { itemSpacing: 8, paddingLeft: 16, paddingRight: 16, paddingTop: 16, paddingBottom: 16, cornerRadius: 16, fill: "#FFFFFF" });
card.appendChild(T("Hello", { family: "Inter", style: "Bold", size: 24 }));
figma.currentPage.appendChild(card);
return { id: card.id, preview: await shot(card, 2) };
```

The app lists each script by its first line, so a leading `// comment` makes the activity log readable.

## Security

Scripts run with full access to the open Figma file. The bridge therefore:

- binds to `127.0.0.1` only;
- rejects requests whose `Host` is not `127.0.0.1`/`localhost` (blocks DNS rebinding);
- rejects browser requests (`Origin` / `Sec-Fetch-Site` headers) on the agent endpoints (`/run`, `GET /result`), so web pages cannot submit scripts — only local tools like curl can;
- admits only the plugin it extracted on the plugin endpoints (`/pull`, `POST /result`): each install generates a random token that is written into the plugin files, so a web page cannot read queued scripts or forge results.

The app's only outbound requests are the update checks and downloads, to `github.com` and GitHub's download host.

A script whose plugin stops responding for 15 seconds (panel closed, `figma.closePlugin()`) is failed rather than left hanging.

Only run scripts from agent sessions you trust.

## Build from source

Requires Go 1.24+; the macOS window needs cgo (Xcode command line tools). The Windows build is pure Go and cross-compiles from macOS.

```bash
make test       # go test -race ./...
make build      # dist/marionette for this machine
make app        # dist/Marionette.app, universal (macOS only)
make release    # macOS and Windows archives (macOS only)
```

Pushing a `v*` tag builds and publishes a GitHub release via `.github/workflows/release.yml`.

## Known limitations

- The Figma plugin must stay open in Figma desktop, in every file an agent should reach
- Cloud-side features of the official MCP are out of scope: `search_design_system`, `get_libraries`, Code Connect, `create_new_file` (the last of those is exempt from the MCP rate limit anyway)
- Results must be JSON-serializable — return node IDs, not node objects
- The macOS app is ad-hoc signed, not notarized; the Windows build is untested on real hardware
- Linux is not supported (Figma has no official Linux desktop app)

## Roadmap

- [ ] Developer ID signing + notarization for macOS; Windows icon and signing
- [x] Multi-file routing
- [ ] WebSocket instead of polling; parallel jobs
- [ ] Figma Community publishing: `networkAccess` declaration and an auth token; drop `enablePrivatePluginApi`, which public plugins may not set (files then match by id or name only)

## License

MIT
