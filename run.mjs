#!/usr/bin/env node
// Submit a script file to the figma bridge and wait for the result.
//
//   node run.mjs <script.js> [--timeout 120]
//
// Prints the result JSON to stdout. Any {$png: base64} value anywhere in the
// result is saved as a PNG under /tmp and replaced by its file path, so
// screenshots taken with helpers.shot() come back as viewable files.

import { readFileSync, writeFileSync } from "node:fs";

const PORT = Number(process.env.FIGMA_BRIDGE_PORT || 3055);
const BASE = `http://127.0.0.1:${PORT}`;

const args = process.argv.slice(2);
const file = args.find((a) => !a.startsWith("--"));
const timeoutIdx = args.indexOf("--timeout");
const timeoutSec = timeoutIdx >= 0 ? Number(args[timeoutIdx + 1]) : 120;
if (!file) {
  console.error("usage: node run.mjs <script.js> [--timeout seconds]");
  process.exit(2);
}

const code = readFileSync(file, "utf8");

const savePngs = (value, id, counter = { n: 0 }) => {
  if (Array.isArray(value)) return value.map((v) => savePngs(v, id, counter));
  if (value && typeof value === "object") {
    if (typeof value.$png === "string") {
      const path = `/tmp/figma-bridge-${id.slice(0, 8)}-${counter.n++}.png`;
      writeFileSync(path, Buffer.from(value.$png, "base64"));
      return path;
    }
    const out = {};
    for (const [k, v] of Object.entries(value)) out[k] = savePngs(v, id, counter);
    return out;
  }
  return value;
};

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

try {
  const runRes = await fetch(`${BASE}/run`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ code }),
  });
  const { id, error } = await runRes.json();
  if (!id) throw new Error(error || "bridge refused the job");

  const deadline = Date.now() + timeoutSec * 1000;
  while (Date.now() < deadline) {
    await sleep(300);
    const r = await fetch(`${BASE}/result?id=${id}`);
    const body = await r.json();
    if (body.pending) continue;
    if (body.ok) {
      console.log(JSON.stringify(savePngs(body.result, id), null, 2));
      process.exit(0);
    } else {
      console.error("SCRIPT ERROR: " + body.error);
      if (body.stack) console.error(body.stack);
      process.exit(1);
    }
  }
  console.error(`timeout after ${timeoutSec}s — is the plugin open in Figma?`);
  process.exit(3);
} catch (e) {
  console.error("BRIDGE ERROR: " + e.message + " — is bridge-server.mjs running?");
  process.exit(4);
}
