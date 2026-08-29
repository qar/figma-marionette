// MM Figma Builder — one-shot script that completes the "MM-Test" design file.
// It finishes the Settings screen (3 remaining groups) and builds the full
// Home screen, reusing the components/variables already created via MCP.
//
// Hard dependencies on node IDs inside the MM-Test file
// (https://www.figma.com/design/ugvP4SXxVrRXLjvgpedclw):
//   0:1     page "Modocus UI 总览"
//   20:136  screen frame "06 设置"
//   7:13    component "SettingsRow"
// Running it in any other file aborts with an error toast; re-running in
// MM-Test is safe (Home is rebuilt from scratch, Settings groups are skipped
// when already present).

(async () => {
  const hex = (h) => {
    const n = parseInt(h.slice(1), 16);
    return { r: ((n >> 16) & 255) / 255, g: ((n >> 8) & 255) / 255, b: (n & 255) / 255 };
  };
  const S = (h, opacity) => [{ type: "SOLID", color: hex(h), ...(opacity !== undefined ? { opacity } : {}) }];

  // Auto-layout frame factory. Vanilla Plugin API has no figma.createAutoLayout.
  const AL = (dir, opts = {}) => {
    const f = figma.createFrame();
    f.layoutMode = dir;
    f.primaryAxisSizingMode = "AUTO";
    f.counterAxisSizingMode = "AUTO";
    f.fills = opts.fill ? S(opts.fill) : [];
    if (opts.name) f.name = opts.name;
    if (opts.itemSpacing !== undefined) f.itemSpacing = opts.itemSpacing;
    for (const k of ["paddingLeft", "paddingRight", "paddingTop", "paddingBottom"]) {
      if (opts[k] !== undefined) f[k] = opts[k];
    }
    if (opts.cornerRadius !== undefined) f.cornerRadius = opts.cornerRadius;
    if (opts.center) f.counterAxisAlignItems = "CENTER";
    return f;
  };

  const T = (chars, opts = {}) => {
    const t = figma.createText();
    t.fontName = { family: opts.family || "Noto Sans SC", style: opts.style || "Regular" };
    t.fontSize = opts.size || 14;
    t.characters = chars;
    t.fills = S(opts.color || "#202622");
    if (opts.ls) t.letterSpacing = { unit: "PIXELS", value: opts.ls };
    return t;
  };

  const SVG = (markup, w, h) => {
    const n = figma.createNodeFromSvg(markup);
    n.resize(w, h);
    return n;
  };

  const divider = (parent) => {
    const d = figma.createRectangle();
    d.resize(100, 1);
    d.fills = S("#F2F4F1");
    parent.appendChild(d);
    d.layoutSizingHorizontal = "FILL";
  };

  try {
    await Promise.all([
      figma.loadFontAsync({ family: "Noto Sans SC", style: "Regular" }),
      figma.loadFontAsync({ family: "Noto Sans SC", style: "Medium" }),
      figma.loadFontAsync({ family: "Noto Sans SC", style: "Bold" }),
      figma.loadFontAsync({ family: "Inter", style: "Bold" }),
    ]);

    const page = await figma.getNodeByIdAsync("0:1");
    if (!page || page.type !== "PAGE") throw new Error("Page 0:1 not found — run this inside the MM-Test file");
    const settingsScr = await figma.getNodeByIdAsync("20:136");
    const rowComp = await figma.getNodeByIdAsync("7:13");
    if (!settingsScr || !rowComp || rowComp.type !== "COMPONENT") {
      throw new Error("Settings screen / SettingsRow component not found — is this the MM-Test file?");
    }

    // ---------- Part A: remaining Settings groups ----------
    let settingsAdded = 0;
    if (!settingsScr.findOne((n) => n.name === "group-AI 助手")) {
      const groups = [
        ["AI 助手", [["Modocus AI", "已订阅"], ["BYOK 模型", "未配置"]], false],
        ["方案", [["Pro 买断", "已解锁"], ["Modocus AI 订阅", "$4.99/月"]], true],
        ["关于", [["版本", "1.0.0 (100)"]], false],
      ];
      for (const [gname, rows, purple] of groups) {
        const gl = T(gname, { style: "Medium", size: 12, color: "#8D9892", ls: 1 });
        settingsScr.appendChild(gl);
        const card = AL("VERTICAL", { name: "group-" + gname, cornerRadius: 14, fill: "#FFFFFF" });
        card.clipsContent = true;
        settingsScr.appendChild(card);
        card.layoutSizingHorizontal = "FILL";
        rows.forEach(([l, v], i) => {
          if (i > 0) divider(card);
          const inst = rowComp.createInstance();
          card.appendChild(inst);
          inst.layoutSizingHorizontal = "FILL";
          inst.findOne((n) => n.name === "label").characters = l;
          inst.findOne((n) => n.name === "value").characters = v;
          if (purple) {
            inst.findOne((n) => n.name === "icon-chip").fills = S("#EDE9FE");
            inst.findOne((n) => n.name === "glyph").strokes = S("#8B5CF6");
          }
          settingsAdded++;
        });
      }
    }

    // ---------- Part B: Home screen (rebuild from scratch each run) ----------
    for (const n of page.children.filter((c) => c.name === "01 主页 Home")) n.remove();

    const scr = AL("VERTICAL", {
      name: "01 主页 Home", itemSpacing: 12,
      paddingLeft: 16, paddingRight: 16, paddingTop: 62,
      cornerRadius: 28, fill: "#F8F9F7",
    });
    scr.resize(390, 844);
    scr.primaryAxisSizingMode = "FIXED";
    scr.counterAxisSizingMode = "FIXED";
    scr.clipsContent = true;
    scr.x = 0;
    scr.y = 1100;
    page.appendChild(scr);

    // Header: title + settings gear
    const hrow = AL("HORIZONTAL", { name: "Header", paddingLeft: 4, paddingRight: 4, center: true });
    scr.appendChild(hrow);
    hrow.layoutSizingHorizontal = "FILL";
    const ht = T("主页", { style: "Bold", size: 24 });
    hrow.appendChild(ht);
    ht.layoutSizingHorizontal = "FILL";
    hrow.appendChild(SVG('<svg width="20" height="20" viewBox="0 0 20 20" fill="none" xmlns="http://www.w3.org/2000/svg"><circle cx="10" cy="10" r="3" stroke="#56625B" stroke-width="1.8"/><path d="M10 1.5 V4 M10 16 V18.5 M18.5 10 H16 M4 10 H1.5 M16 4 L14.2 5.8 M5.8 14.2 L4 16 M16 16 L14.2 14.2 M5.8 5.8 L4 4" stroke="#56625B" stroke-width="1.8" stroke-linecap="round"/></svg>', 20, 20));

    // ExecutionFocusPanel: running task card (brand tint + brand border)
    const run = AL("VERTICAL", {
      name: "RunningTaskCard", itemSpacing: 6,
      paddingLeft: 16, paddingRight: 16, paddingTop: 14, paddingBottom: 14,
      cornerRadius: 16, fill: "#FFFCEB",
    });
    run.strokes = S("#FFD000");
    run.strokeWeight = 1;
    scr.appendChild(run);
    run.layoutSizingHorizontal = "FILL";
    const rlabel = AL("HORIZONTAL", { itemSpacing: 6, center: true });
    run.appendChild(rlabel);
    const rdot = figma.createEllipse();
    rdot.resize(7, 7);
    rdot.fills = S("#E6BB00");
    rlabel.appendChild(rdot);
    rlabel.appendChild(T("计时中", { style: "Medium", size: 12, color: "#806800" }));
    run.appendChild(T("00:18:42", { family: "Inter", style: "Bold", size: 30 }));
    run.appendChild(T("撰写 Q3 周报草稿", { size: 15 }));
    run.appendChild(T("预估 45 分钟 · 今天到期", { size: 12, color: "#6F7A74" }));

    // ExecutionFocusPanel: pinned task card with ECG pulse
    const pin = AL("HORIZONTAL", {
      name: "PinnedTaskCard", itemSpacing: 10,
      paddingLeft: 16, paddingRight: 16, paddingTop: 12, paddingBottom: 12,
      cornerRadius: 16, fill: "#FFFFFF", center: true,
    });
    scr.appendChild(pin);
    pin.layoutSizingHorizontal = "FILL";
    const pt = T("整理 9 月产品发布计划", { size: 15 });
    pin.appendChild(pt);
    pt.layoutSizingHorizontal = "FILL";
    pin.appendChild(SVG('<svg width="24" height="14" viewBox="0 0 24 14" fill="none" xmlns="http://www.w3.org/2000/svg"><path d="M0 8 H6 L9 2 L12 12 L14 8 H24" stroke="#10B981" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg>', 24, 14));

    // TodayPlanCard
    const plan = AL("VERTICAL", {
      name: "TodayPlanCard", itemSpacing: 4,
      paddingLeft: 16, paddingRight: 16, paddingTop: 14, paddingBottom: 14,
      cornerRadius: 16, fill: "#FFFFFF",
    });
    scr.appendChild(plan);
    plan.layoutSizingHorizontal = "FILL";
    plan.appendChild(T("今日计划", { style: "Medium", size: 13, color: "#6F7A74" }));
    plan.appendChild(T("5 项 · 约 2.5 小时", { style: "Bold", size: 20 }));
    plan.appendChild(T("3 项可在 30 分钟内完成", { size: 12, color: "#8D9892" }));

    // ForecastWeekStrip: 7 day pills, today highlighted
    const strip = AL("HORIZONTAL", {
      name: "ForecastWeekStrip",
      paddingLeft: 14, paddingRight: 14, paddingTop: 12, paddingBottom: 12,
      cornerRadius: 16, fill: "#FFFFFF",
    });
    strip.primaryAxisAlignItems = "SPACE_BETWEEN";
    scr.appendChild(strip);
    strip.layoutSizingHorizontal = "FILL";
    const days = [["六", "29", 1, true], ["日", "30", 1, false], ["一", "31", 0, false], ["二", "1", 2, false], ["三", "2", 0, false], ["四", "3", 1, false], ["五", "4", 0, false]];
    for (const [wd, date, count, today] of days) {
      const pill = AL("VERTICAL", { name: "DayPill", itemSpacing: 4 });
      pill.counterAxisAlignItems = "CENTER";
      pill.appendChild(T(wd, { size: 11, color: "#8D9892" }));
      const circle = AL("HORIZONTAL", { cornerRadius: 16 });
      circle.primaryAxisAlignItems = "CENTER";
      circle.counterAxisAlignItems = "CENTER";
      circle.resize(32, 32);
      circle.primaryAxisSizingMode = "FIXED";
      circle.counterAxisSizingMode = "FIXED";
      if (today) circle.fills = S("#29332D", 0.12);
      circle.appendChild(T(date, { size: 14, style: today ? "Medium" : "Regular" }));
      pill.appendChild(circle);
      pill.appendChild(T(count > 0 ? String(count) : "·", { size: 10, color: count > 0 ? "#56625B" : "#CCD2CF" }));
      strip.appendChild(pill);
    }

    // StatGrid: 3 equal cards
    const stats = AL("HORIZONTAL", { name: "StatGrid", itemSpacing: 8 });
    scr.appendChild(stats);
    stats.layoutSizingHorizontal = "FILL";
    for (const [num, lbl] of [["3", "收件箱"], ["5", "今天"], ["1", "逾期"]]) {
      const card = AL("VERTICAL", {
        name: "StatCard", itemSpacing: 2,
        paddingLeft: 14, paddingRight: 14, paddingTop: 12, paddingBottom: 12,
        cornerRadius: 12, fill: "#F2F4F1",
      });
      stats.appendChild(card);
      card.layoutSizingHorizontal = "FILL";
      card.appendChild(T(num, { family: "Inter", style: "Bold", size: 22 }));
      card.appendChild(T(lbl, { size: 12, color: "#6F7A74" }));
    }

    // ProjectCard
    const pc = AL("HORIZONTAL", {
      name: "ProjectCard", itemSpacing: 10,
      paddingLeft: 14, paddingRight: 14, paddingTop: 12, paddingBottom: 12,
      cornerRadius: 14, fill: "#FFFFFF", center: true,
    });
    scr.appendChild(pc);
    pc.layoutSizingHorizontal = "FILL";
    const pdot = figma.createEllipse();
    pdot.resize(10, 10);
    pdot.fills = S("#3B82F6");
    pc.appendChild(pdot);
    const pname = T("产品发布", { style: "Medium", size: 15 });
    pc.appendChild(pname);
    pname.layoutSizingHorizontal = "FILL";
    pc.appendChild(T("8 项待办", { size: 13, color: "#8D9892" }));
    pc.appendChild(SVG('<svg width="16" height="16" viewBox="0 0 16 16" fill="none" xmlns="http://www.w3.org/2000/svg"><path d="M6 3.5 L10.5 8 L6 12.5" stroke="#AEB7B2" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg>', 16, 16));

    // View nav grid (2 x 2 tiles)
    const vlabel = T("视图", { style: "Medium", size: 12, color: "#8D9892", ls: 1 });
    scr.appendChild(vlabel);
    const tiles = [
      [["今天", "5 项", '<svg width="18" height="18" viewBox="0 0 18 18" fill="none" xmlns="http://www.w3.org/2000/svg"><circle cx="9" cy="9" r="3.5" stroke="#414B45" stroke-width="1.6"/><path d="M9 1 V3 M9 15 V17 M17 9 H15 M3 9 H1 M14.7 3.3 L13.3 4.7 M4.7 13.3 L3.3 14.7 M14.7 14.7 L13.3 13.3 M4.7 4.7 L3.3 3.3" stroke="#414B45" stroke-width="1.6" stroke-linecap="round"/></svg>'],
       ["进行中", "3 项", '<svg width="18" height="18" viewBox="0 0 18 18" fill="none" xmlns="http://www.w3.org/2000/svg"><path d="M1 9 H5 L7.5 4 L10.5 14 L12.5 9 H17" stroke="#414B45" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg>']],
      [["等待中", "2 项", '<svg width="18" height="18" viewBox="0 0 18 18" fill="none" xmlns="http://www.w3.org/2000/svg"><path d="M4 1.5 H14 M4 16.5 H14 M5 1.5 C5 9 13 9 13 16.5 M13 1.5 C13 9 5 9 5 16.5" stroke="#414B45" stroke-width="1.6" stroke-linecap="round"/></svg>'],
       ["全部", "28 项", '<svg width="18" height="18" viewBox="0 0 18 18" fill="none" xmlns="http://www.w3.org/2000/svg"><path d="M2 5 L9 1.5 L16 5 L9 8.5 Z M2 9.5 L9 13 L16 9.5 M2 13.5 L9 17 L16 13.5" stroke="#414B45" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg>']],
    ];
    for (const rowTiles of tiles) {
      const grow = AL("HORIZONTAL", { itemSpacing: 8 });
      scr.appendChild(grow);
      grow.layoutSizingHorizontal = "FILL";
      for (const [lbl, sub, icon] of rowTiles) {
        const tile = AL("HORIZONTAL", {
          name: "ViewNavTile", itemSpacing: 10,
          paddingLeft: 12, paddingRight: 12,
          cornerRadius: 8, fill: "#F2F4F1", center: true,
        });
        grow.appendChild(tile);
        tile.resize(100, 64);
        tile.layoutSizingHorizontal = "FILL";
        tile.appendChild(SVG(icon, 18, 18));
        const col = AL("VERTICAL", { itemSpacing: 2 });
        tile.appendChild(col);
        col.appendChild(T(lbl, { style: "Medium", size: 13 }));
        col.appendChild(T(sub, { size: 11, color: "#8D9892" }));
      }
    }

    // More list: reuse SettingsRow instances, value hidden
    const mlabel = T("更多", { style: "Medium", size: 12, color: "#8D9892", ls: 1 });
    scr.appendChild(mlabel);
    const more = AL("VERTICAL", { name: "MoreList", cornerRadius: 14, fill: "#FFFFFF" });
    more.clipsContent = true;
    scr.appendChild(more);
    more.layoutSizingHorizontal = "FILL";
    ["统计", "会议导入", "设置"].forEach((l, i) => {
      if (i > 0) divider(more);
      const inst = rowComp.createInstance();
      more.appendChild(inst);
      inst.layoutSizingHorizontal = "FILL";
      inst.findOne((n) => n.name === "label").characters = l;
      inst.findOne((n) => n.name === "value").visible = false;
    });

    try {
      figma.currentPage = page;
      figma.viewport.scrollAndZoomIntoView([scr]);
    } catch (e) { /* page switch is best-effort */ }

    figma.notify(`Done: Home rebuilt, ${settingsAdded} settings rows added`);
  } catch (e) {
    figma.notify("Failed: " + e.message, { error: true });
  }
  figma.closePlugin();
})();
