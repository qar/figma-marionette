# MM Figma Builder

用 Figma **Plugin API**（免费、无调用限额）向 Figma 文件写入设计稿的本地插件。当前版本是针对 **MM-Test** 文件的一次性补建脚本；目录结构已按可独立建仓、后续演进为通用「AI 脚本桥」插件的方向组织。

## 当前功能

在 [MM-Test](https://www.figma.com/design/ugvP4SXxVrRXLjvgpedclw) 文件中：

1. 补齐 `06 设置` 屏剩余三组（AI 助手 / 方案 / 关于），复用已有 `SettingsRow` 组件（`7:13`）；
2. 从零构建 `01 主页 Home` 屏（执行焦点卡、今日计划、预告周条、Stat 三格、项目卡、视图格、更多列表）。

脚本可重复运行：主页每次删除重建，设置组已存在时跳过。仅在 MM-Test 中生效——依赖硬编码 node ID（页面 `0:1`、设置屏 `20:136`、SettingsRow `7:13`），在其他文件运行会报错退出，不会误改。

## 使用方法

1. 在 **Figma 桌面端**打开 MM-Test 文件；
2. 菜单 **Plugins → Development → Import plugin from manifest…**，选择本目录的 `manifest.json`；
3. 运行 **MM Figma Builder**，右下角出现「完成」toast 即结束。

## 目录结构

```
figma-mm-builder/
├── manifest.json   # 插件清单（editorType: figma）
├── code.js         # 主脚本（原生 Plugin API，无构建步骤）
└── README.md
```

## 设计 Token 约定

颜色/字号与 App 源码 `src/global.css` 对齐（MM-Test 内已有同名 Figma 变量集合 "Modocus Tokens"）。中文用 Noto Sans SC，数字用 Inter（App 实际字体为 Inter，中文回退系统苹方）。

## 后续演进方向

- [ ] 抽掉硬编码 node ID，改为运行时按名称查找 / UI 选择目标；
- [ ] 增加 WebSocket / 轮询桥模式：从本地端口接收 AI 生成的脚本执行（参考 cursor-talk-to-figma-mcp 架构），彻底替代官方 MCP 网关的写入路径；
- [ ] TypeScript + 构建链（`@figma/plugin-typings`），再考虑 Community 发布。
