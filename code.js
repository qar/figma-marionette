// MM Figma Builder — bridge mode.
// Keeps polling a local bridge server (via ui.html) and executes received
// scripts against the Figma Plugin API. This replaces the official Figma MCP
// write path (use_figma) with an unlimited local channel.
//
// Script contract (same conventions as MCP use_figma):
//   - plain JavaScript, top-level `await` allowed (wrapped in AsyncFunction)
//   - `return` a JSON-serializable value to send data back
//   - a `helpers` object is in scope: { hex, S, AL, T, SVG, shot }

figma.showUI(__html__, { width: 300, height: 120 });

const hex = (h) => {
  const n = parseInt(h.slice(1), 16);
  return { r: ((n >> 16) & 255) / 255, g: ((n >> 8) & 255) / 255, b: (n & 255) / 255 };
};
const S = (h, opacity) => [{ type: "SOLID", color: hex(h), ...(opacity !== undefined ? { opacity } : {}) }];

// Auto-layout frame factory (vanilla API has no figma.createAutoLayout).
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

// Text factory. Caller is responsible for loading the font first.
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
  if (w && h) n.resize(w, h);
  return n;
};

// Export a node as PNG. The result is tagged so run.mjs saves it to a file.
const shot = async (node, scale = 1) => {
  const bytes = await node.exportAsync({ format: "PNG", constraint: { type: "SCALE", value: scale } });
  return { $png: figma.base64Encode(bytes) };
};

const helpers = { hex, S, AL, T, SVG, shot };

figma.ui.onmessage = async (m) => {
  if (!m || m.type !== "run") return;
  let out;
  try {
    const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor;
    const fn = new AsyncFunction("figma", "helpers", '"use strict";\n' + m.code);
    let result = await fn(figma, helpers);
    // Strip anything JSON can't carry (nodes, functions, cycles).
    try {
      result = result === undefined ? null : JSON.parse(JSON.stringify(result));
    } catch (_) {
      result = String(result);
    }
    out = { type: "result", id: m.id, ok: true, result };
  } catch (e) {
    out = {
      type: "result", id: m.id, ok: false,
      error: (e && e.message) || String(e),
      stack: e && e.stack ? String(e.stack).split("\n").slice(0, 4).join("\n") : undefined,
    };
  }
  figma.ui.postMessage(out);
};
