// Local bridge server between a CLI (Claude Code / any tool) and the
// MM Figma Builder plugin running inside Figma desktop.
//
//   CLI     POST /run     {code}          -> {id}
//   plugin  GET  /pull                    -> {job: {id, code} | null}
//   plugin  POST /result  {id, ok, ...}   -> {ok: true}
//   CLI     GET  /result?id=...           -> stored result | {pending: true}
//
// Zero dependencies. Binds to 127.0.0.1 only — scripts are eval'ed inside
// the Figma plugin sandbox, so never expose this port beyond localhost.

import http from "node:http";
import crypto from "node:crypto";

const PORT = Number(process.env.FIGMA_BRIDGE_PORT || 3055);
const queue = [];
const results = new Map();

const json = (res, code, body) => {
  res.writeHead(code, {
    "Content-Type": "application/json",
    "Access-Control-Allow-Origin": "*",
    "Access-Control-Allow-Headers": "Content-Type",
    "Access-Control-Allow-Methods": "GET, POST, OPTIONS",
  });
  res.end(JSON.stringify(body));
};

const readBody = (req) =>
  new Promise((resolve, reject) => {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => {
      try {
        resolve(JSON.parse(Buffer.concat(chunks).toString("utf8") || "{}"));
      } catch (e) {
        reject(e);
      }
    });
    req.on("error", reject);
  });

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, `http://127.0.0.1:${PORT}`);
  try {
    if (req.method === "OPTIONS") {
      json(res, 204, {});
    } else if (req.method === "POST" && url.pathname === "/run") {
      const body = await readBody(req);
      if (typeof body.code !== "string" || !body.code.trim()) {
        return json(res, 400, { error: "code (string) is required" });
      }
      const id = crypto.randomUUID();
      queue.push({ id, code: body.code });
      json(res, 200, { id, queued: queue.length });
    } else if (req.method === "GET" && url.pathname === "/pull") {
      json(res, 200, { job: queue.shift() || null });
    } else if (req.method === "POST" && url.pathname === "/result") {
      const body = await readBody(req);
      if (!body.id) return json(res, 400, { error: "id is required" });
      results.set(body.id, body);
      json(res, 200, { ok: true });
    } else if (req.method === "GET" && url.pathname === "/result") {
      const id = url.searchParams.get("id");
      json(res, 200, results.get(id) || { pending: true });
    } else if (req.method === "GET" && url.pathname === "/health") {
      json(res, 200, { ok: true, queued: queue.length, stored: results.size });
    } else {
      json(res, 404, { error: "not found" });
    }
  } catch (e) {
    json(res, 500, { error: e.message });
  }
});

server.listen(PORT, "127.0.0.1", () => {
  console.log(`figma bridge listening on http://127.0.0.1:${PORT}`);
});
