#!/usr/bin/env node
// bk-request-log-shim.mjs — runs the real Buildkite CLI (bk) and logs every
// Buildkite API request it makes, one JSON line per request, to the same
// request log the MCP server writes (--request-log). babystand.sh installs a
// `bk` wrapper first on the agent's PATH that execs this file with:
//   BK_SHIM_REAL_BK      path of the real bk binary
//   BK_SHIM_REQUEST_LOG  request log to append to (JSON lines)
//
// How: for each bk invocation, start a plain-HTTP proxy on 127.0.0.1 (random
// port) and point bk at it through its own endpoint settings —
// BUILDKITE_REST_API_ENDPOINT and BUILDKITE_GRAPHQL_ENDPOINT — which every
// bk API client is built from (pkg/cmd/factory, bk v3.59.1; `bk api` reuses
// the same base URL). The proxy forwards each request unchanged to the real
// endpoint over HTTPS and logs it. bk's stdin/stdout/stderr are inherited
// untouched and its exit code is passed through, so the agent sees normal bk.
// Artifact downloads: `bk artifacts download` fetches each file from the
// artifact's absolute download_url, so for that command the proxy rewrites
// those URLs in the artifact list to point back at itself (see
// rewriteDownloadURLs) and the download requests are logged too.
// Not seen: other requests bk sends to absolute URLs from API responses
// (e.g. the S3 redirect after a download — not a Buildkite API request),
// and bk's non-API HTTP (self-update, skill downloads).
//
// Log lines (source "bk"):
//   {"source":"bk","type":"request", invocation, argv, api: rest|graphql,
//    method, url, status, duration_ms, bytes, graphql_operation?, error?}
//   {"source":"bk","type":"invocation", invocation, argv, exit_code,
//    duration_ms, requests}   — one per bk run, even with zero requests
import http from "node:http";
import https from "node:https";
import { spawn } from "node:child_process";
import fs from "node:fs";
import crypto from "node:crypto";

// Exit code for a bk killed by a signal: 128 + signal number, as a shell reports it.
const os_signals = { SIGHUP: 1, SIGINT: 2, SIGKILL: 9, SIGTERM: 15 };

const realBk = process.env.BK_SHIM_REAL_BK;
const logPath = process.env.BK_SHIM_REQUEST_LOG;
const argv = process.argv.slice(2);

// The shim's own settings never reach bk.
const childEnv = { ...process.env };
delete childEnv.BK_SHIM_REAL_BK;
delete childEnv.BK_SHIM_REQUEST_LOG;

if (!realBk) {
  process.stderr.write("bk shim: BK_SHIM_REAL_BK is not set\n");
  process.exit(127);
}

// Without a log path, behave exactly like bk.
if (!logPath) {
  runBk(childEnv, () => {});
} else {
  startProxyAndRun();
}

function writeLog(record) {
  try {
    fs.appendFileSync(logPath, JSON.stringify({ source: "bk", ...record }) + "\n");
  } catch {
    // Logging must never break the agent's bk call.
  }
}

function runBk(env, onExit) {
  const started = Date.now();
  const child = spawn(realBk, argv, { stdio: "inherit", env });
  for (const sig of ["SIGINT", "SIGTERM", "SIGHUP"]) {
    process.on(sig, () => child.kill(sig));
  }
  child.on("error", (err) => {
    process.stderr.write(`bk shim: failed to run ${realBk}: ${err.message}\n`);
    onExit(127, Date.now() - started);
    process.exit(127);
  });
  child.on("exit", (code, signal) => {
    const exitCode = code ?? 128 + (signal ? (os_signals[signal] ?? 0) : 0);
    onExit(exitCode, Date.now() - started);
    process.exit(exitCode);
  });
}


// isArtifactsDownload reports whether args run `bk artifacts download` (flags
// before the subcommand words are skipped).
function isArtifactsDownload(args) {
  const words = [];
  for (const a of args) {
    if (a.startsWith("-")) {
      if (words.length === 0) continue;
      break;
    }
    words.push(a);
    if (words.length === 2) break;
  }
  return /^artifacts?$/.test(words[0] || "") && words[1] === "download";
}

function startProxyAndRun() {
  const invocation = crypto.randomUUID();
  // The upstreams bk would have used without the shim (honouring any override
  // already in its environment).
  const upstreams = {
    rest: new URL(process.env.BUILDKITE_REST_API_ENDPOINT || "https://api.buildkite.com/"),
    graphql: new URL(process.env.BUILDKITE_GRAPHQL_ENDPOINT || "https://graphql.buildkite.com/v1"),
  };
  let requests = 0;
  // Set once the proxy is listening (before bk starts, so before any request).
  let localRestBase = "";
  // `bk artifacts download` fetches each file from the artifact's own
  // download_url — an absolute API URL taken from the artifact list, which
  // would skip this proxy. For that command only, rewrite those URLs in the
  // proxied JSON responses to point back at the proxy, so the downloads are
  // logged too. Safe there because the command never prints the URLs (it
  // prints "Downloaded: <path>"); other commands, e.g. `bk artifacts list`,
  // may show them to the agent, so their responses are left untouched.
  const rewriteDownloadURLs = isArtifactsDownload(argv);
  const restUpstreamBase = upstreams.rest.origin + upstreams.rest.pathname;

  // Point absolute REST URLs (plain and JSON-escaped "\/" forms) at the proxy.
  function rewriteURLs(text) {
    const escaped = (u) => u.replace(/\//g, "\\/");
    return text
      .split(restUpstreamBase).join(localRestBase)
      .split(escaped(restUpstreamBase)).join(escaped(localRestBase));
  }

  const server = http.createServer((req, res) => {
    const api = req.url.startsWith("/__graphql/") ? "graphql" : "rest";
    const upstream = upstreams[api];
    const path = req.url.replace(/^\/__(graphql|rest)/, "");
    const target = new URL(path, upstream.origin);
    const started = Date.now();
    const chunks = [];

    req.on("data", (c) => chunks.push(c));
    req.on("end", () => {
      const body = Buffer.concat(chunks);
      const record = {
        type: "request", invocation, argv, api,
        time: new Date(started).toISOString(),
        method: req.method,
        url: target.toString(),
      };
      if (api === "graphql") {
        try {
          const op = JSON.parse(body.toString("utf8")).operationName;
          if (op) record.graphql_operation = op;
        } catch {
          // Not JSON — log the request without an operation name.
        }
      }
      const headers = { ...req.headers, host: target.host };
      const rewrite = rewriteDownloadURLs && api === "rest";
      // Ask for a plain body, so a JSON response can be rewritten.
      if (rewrite) delete headers["accept-encoding"];
      const client = target.protocol === "http:" ? http : https;
      const upstreamReq = client.request(target, { method: req.method, headers }, (upstreamRes) => {
        let bytes = 0;
        const isJSON = String(upstreamRes.headers["content-type"] || "").includes("json");
        if (rewrite && isJSON && !upstreamRes.headers["content-encoding"]) {
          const parts = [];
          upstreamRes.on("data", (c) => { bytes += c.length; parts.push(c); });
          upstreamRes.on("end", () => {
            const out = Buffer.from(rewriteURLs(Buffer.concat(parts).toString("utf8")), "utf8");
            const outHeaders = { ...upstreamRes.headers, "content-length": String(out.length) };
            delete outHeaders["transfer-encoding"];
            requests++;
            writeLog({ ...record, status: upstreamRes.statusCode, duration_ms: Date.now() - started, bytes });
            res.writeHead(upstreamRes.statusCode ?? 502, outHeaders);
            res.end(out);
          });
          return;
        }
        res.writeHead(upstreamRes.statusCode ?? 502, upstreamRes.headers);
        upstreamRes.on("data", (c) => { bytes += c.length; res.write(c); });
        upstreamRes.on("end", () => {
          requests++;
          writeLog({ ...record, status: upstreamRes.statusCode, duration_ms: Date.now() - started, bytes });
          res.end();
        });
      });
      upstreamReq.on("error", (err) => {
        requests++;
        writeLog({ ...record, error: err.message, duration_ms: Date.now() - started });
        res.writeHead(502, { "content-type": "text/plain" });
        res.end(`bk shim: upstream request failed: ${err.message}\n`);
      });
      upstreamReq.end(body);
    });
  });

  server.listen(0, "127.0.0.1", () => {
    const { port } = server.address();
    const local = `http://127.0.0.1:${port}`;
    localRestBase = `${local}/__rest${upstreams.rest.pathname}`;
    const env = {
      ...childEnv,
      BUILDKITE_REST_API_ENDPOINT: `${local}/__rest${upstreams.rest.pathname}`,
      BUILDKITE_GRAPHQL_ENDPOINT: `${local}/__graphql${upstreams.graphql.pathname}`,
    };
    runBk(env, (exitCode, durationMs) => {
      writeLog({ type: "invocation", invocation, argv, exit_code: exitCode, duration_ms: durationMs, requests });
    });
  });
}
