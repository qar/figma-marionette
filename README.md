# MM Figma Builder

用 Figma **Plugin API**（免费、无调用限额）让 AI / 命令行直接读写 Figma 文件的本地桥插件，替代官方 Figma MCP 的写入通道（官方 MCP 在 Starter 计划下每月仅 20 次调用）。

## 架构

```
Claude Code / CLI                bridge-server.mjs              Figma 桌面端插件
  node run.mjs xx.js  ── POST /run ──▶  队列  ◀── GET /pull（500ms 轮询）── ui.html
  轮询 GET /result    ◀── 存储  ◀────────── POST /result ─────────── code.js 执行脚本
```

- `bridge-server.mjs` — 零依赖 Node 服务，只绑定 127.0.0.1:3055
- `code.js` + `ui.html` — 插件本体：UI 轮询桥服务，主线程用 AsyncFunction 执行收到的脚本
- `run.mjs` — 命令行提交脚本文件并等待结果；结果里的 `{$png: base64}` 自动存为 /tmp 下的 PNG 并替换为路径
- `scripts/` — 历史/示例脚本（`mm-test-oneshot.js` 是最初的一次性补建版本，已完成使命）

## 使用

```bash
# 1. 启动桥服务（常驻）
node bridge-server.mjs

# 2. Figma 桌面端：Plugins → Development → Import plugin from manifest…（首次）
#    之后每个工作会话运行一次 "MM Figma Builder"，面板显示「已连接 · 空闲」即可
#    ⚠️ 插件面板保持打开，关掉即断桥

# 3. 提交脚本
node run.mjs my-script.js            # 默认 120s 超时
node run.mjs my-script.js --timeout 300
```

## 脚本约定（与官方 MCP use_figma 一致）

- 顶层可用 `await`，用 `return` 返回 JSON 可序列化数据
- 直接使用 `figma` 全局对象（完整 Plugin API）
- 额外注入 `helpers`：
  - `helpers.hex("#RRGGBB")` → Figma 颜色对象（0-1 范围）
  - `helpers.S("#RRGGBB", opacity?)` → SOLID fills 数组
  - `helpers.AL(dir, opts)` → 建好 auto-layout 的 Frame（原生 API 无 createAutoLayout）
  - `helpers.T(chars, opts)` → 文本节点（**字体需先 loadFontAsync**）
  - `helpers.SVG(markup, w, h)` → 从 SVG 建矢量
  - `await helpers.shot(node, scale?)` → 节点截图，经 run.mjs 落盘为 PNG 路径

示例：

```js
await figma.loadFontAsync({ family: "Inter", style: "Bold" });
const { AL, T, shot } = helpers;
const card = AL("VERTICAL", { itemSpacing: 8, paddingLeft: 16, paddingRight: 16, paddingTop: 16, paddingBottom: 16, cornerRadius: 16, fill: "#FFFFFF" });
card.appendChild(T("Hello", { family: "Inter", style: "Bold", size: 24 }));
figma.currentPage.appendChild(card);
return { id: card.id, preview: await shot(card, 2) };
```

## 安全

脚本在插件沙箱内 eval，桥服务只监听 127.0.0.1 且无鉴权——**不要**把端口暴露到局域网/公网，只在自己信任的会话里往桥里投递脚本。

## 已知边界

- 插件必须在 Figma 桌面端保持打开才能执行；操作的是**当前打开的文件**
- 官方 MCP 独有的云端能力不在此列：`search_design_system`（跨库搜索）、Code Connect 映射、`create_new_file`——这些仍走官方 MCP（读操作限额独立于写入，且 `create_new_file`/`whoami` 免限额）
- 结果必须 JSON 可序列化（节点请返回 id，不要返回节点对象）

## Roadmap

- [ ] TypeScript + `@figma/plugin-typings` 构建链
- [ ] WebSocket 替代轮询；多文件/多任务路由
- [ ] Community 发布前补 `networkAccess` 声明与鉴权 token
