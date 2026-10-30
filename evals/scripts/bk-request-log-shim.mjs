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
// Artifact downloads: `bk artifacts download` and `bk build download` fetch
// each file from the artifact's absolute download_url, so for those commands
// the proxy rewrites those URLs in the artifact list to point back at itself
// (see rewriteDownloadURLs) and the download requests are logged too.
// Not seen: other requests bk sends to absolute URLs from API responses
// (e.g. the S3 redirect after a download — not a Buildkite API request),
// and bk's non-API HTTP (self-update, skill downloads).
//
// Log lines (source "bk"):
//   {"source":"bk","type":"request", invocation, argv, api: rest|graphql,
//    method, url, status, duration_ms, bytes, graphql_operation?, error?}
//   {"source":"bk","type":"invocation", invocation, argv, exit_code,
//    duration_ms, requests}   — one per bk run, even with zero requests
// argv contains recognized command words only, never flags or argument values.
// URLs omit userinfo, queries and fragments; errors retain codes, not messages.
import http from "node:http";
import https from "node:https";
import { spawn } from "node:child_process";
import { BlockList, isIP } from "node:net";
import fs from "node:fs";
import crypto from "node:crypto";

// Exit code for a bk killed by a signal: 128 + signal number, as a shell reports it.
const os_signals = { SIGHUP: 1, SIGINT: 2, SIGKILL: 9, SIGTERM: 15 };

const realBk = process.env.BK_SHIM_REAL_BK;
const logPath = process.env.BK_SHIM_REQUEST_LOG;
const argv = process.argv.slice(2);
// An allowlist, not a secret-name denylist: arbitrary flags, endpoints,
// headers, bodies and positional arguments can all contain credentials.
const globalFlags = new Set(["--quiet", "-q", "--yes", "-y", "--no-input", "--no-pager", "--debug", "--help", "-h"]);
const words = argv.filter((arg) => !globalFlags.has(arg));
const groups = new Set(["agent", "artifacts", "artifact", "auth", "build", "cache", "cluster", "maintainer", "queue", "secret", "skill", "team", "config", "job", "organization", "org", "pipeline", "package", "preflight", "user"]);
const subcommands = new Set(["install", "run", "pause", "list", "ls", "resume", "stop", "view", "download", "login", "logout", "status", "switch", "use", "token", "create", "new", "cancel", "rebuild", "watch", "registry", "update", "delete", "rm", "get", "migrate", "add", "set", "unset", "log", "reprioritize", "priority", "retry", "ssh", "unblock", "vnc", "copy", "cp", "convert", "validate", "push", "cleanup", "invite"]);
const loggedArgv = groups.has(words[0])
  ? [words[0], ...(subcommands.has(words[1]) ? [words[1]] : [])]
  : [new Set(["api", "browse", "init", "update", "version"]).has(words[0]) ? words[0] : "[redacted]"];

// The shim's own settings never reach bk.
const childEnv = { ...process.env };
delete childEnv.BK_SHIM_REAL_BK;
delete childEnv.BK_SHIM_REQUEST_LOG;

if (!realBk) {
  process.stderr.write("bk shim: BK_SHIM_REAL_BK is not set\n");
  process.exit(127);
}

// Without a log path, behave exactly like bk.
const [major, minor] = process.versions.node.split(".").map(Number);
const supportsEnvProxy = major > 24 || (major === 24 && minor >= 5) || (major === 22 && minor >= 21);
if (!logPath) {
  runBk(childEnv, () => {});
} else if (!supportsEnvProxy) {
  process.stderr.write("bk shim: Node.js 22.21 or 24.5+ required for proxy-aware logging; running bk without logging\n");
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


// isDownload reports whether args run a command that downloads artifacts
// from their absolute download_url: `bk artifacts download` or
// `bk build download`. Flags are skipped wherever they sit among the command
// words (bk's global flags, e.g. --quiet, take no value).
function isDownload(args) {
  const words = args.filter((a) => !a.startsWith("-")).slice(0, 2);
  return /^(artifacts?|build)$/.test(words[0] || "") && words[1] === "download";
}

function startProxyAndRun() {
  const invocation = crypto.randomUUID();
  // The upstreams bk would have used without the shim (honouring any override
  // already in its environment).
  const upstreams = {
    rest: new URL(process.env.BUILDKITE_REST_API_ENDPOINT || "https://api.buildkite.com/"),
    graphql: new URL(process.env.BUILDKITE_GRAPHQL_ENDPOINT || "https://graphql.buildkite.com/v1"),
  };
  // Match Go's uppercase-first environment precedence. These agents apply
  // proxies only to upstream traffic, not bk's loopback connection to us.
  const proxyEnv = {
    HTTP_PROXY: process.env.HTTP_PROXY || process.env.http_proxy,
    HTTPS_PROXY: process.env.HTTPS_PROXY || process.env.https_proxy,
  };
  // Match Go's NO_PROXY rules ourselves: Node differs on CIDRs, bare-domain
  // suffixes, port-qualified suffixes and automatic localhost/loopback bypass.
  const bypassIPs = new BlockList();
  bypassIPs.addSubnet("127.0.0.0", 8, "ipv4");
  bypassIPs.addAddress("::1", "ipv6");
  const bypassHosts = [];
  const noProxy = process.env.NO_PROXY || process.env.no_proxy || "";
  for (const entry of noProxy.split(",").map((s) => s.trim().toLowerCase()).filter(Boolean)) {
    const subnet = entry.match(/^(.+)\/(\d+)$/);
    if (subnet && isIP(subnet[1])) {
      try { bypassIPs.addSubnet(subnet[1], Number(subnet[2]), isIP(subnet[1]) === 4 ? "ipv4" : "ipv6"); }
      catch { /* Go ignores invalid NO_PROXY entries too. */ }
    } else {
      const match = isIP(entry) ? [entry, entry, ""] : entry.match(/^(\[[^\]]+\]|[^:]+)(?::(\d+))?$/);
      if (match) bypassHosts.push({ host: match[1].replace(/^\[|\]$/g, "").replace(/^\*\./, "."), port: match[2] || "" });
    }
  }
  const agents = {
    "http:": new http.Agent({ proxyEnv }),
    "https:": new https.Agent({ proxyEnv }),
  };
  let requests = 0;
  // Requests sent upstream whose response hasn't finished yet: their logOnce.
  const pending = new Set();
  // Set once the proxy is listening (before bk starts, so before any request).
  let localRestBase = "";
  // `bk artifacts download` and `bk build download` fetch each file from the
  // artifact's own download_url — an absolute API URL taken from the artifact
  // list, which would skip this proxy. For those commands only, rewrite those
  // download_url values in the proxied JSON responses to point back at the
  // proxy, so the downloads are logged too. Safe there because they never print the URLs
  // (they print the downloaded paths); other commands, e.g. `bk artifacts
  // list`, may show them to the agent, so their responses are left untouched.
  const rewriteDownloadURLs = isDownload(argv);
  const restUpstreamBase = upstreams.rest.origin + upstreams.rest.pathname;

  // Point artifacts' download_url values at the proxy, and nothing else: the
  // same response types also carry content the agent reads (e.g. `bk build
  // download` saves job logs fetched as JSON), which must stay untouched.
  // Returns the body unchanged when it isn't JSON or has no such URL.
  function rewriteDownloadURLsIn(body) {
    let data;
    try { data = JSON.parse(body.toString("utf8")); } catch { return body; }
    let changed = false;
    const fix = (a) => {
      if (a && typeof a === "object" && typeof a.download_url === "string"
          && a.download_url.startsWith(restUpstreamBase)) {
        a.download_url = localRestBase + a.download_url.slice(restUpstreamBase.length);
        changed = true;
      }
    };
    if (Array.isArray(data)) data.forEach(fix); else fix(data);
    return changed ? Buffer.from(JSON.stringify(data), "utf8") : body;
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
      const loggedURL = new URL(target);
      loggedURL.username = "";
      loggedURL.password = "";
      loggedURL.search = "";
      loggedURL.hash = "";
      const record = {
        type: "request", invocation, argv: loggedArgv, api,
        time: new Date(started).toISOString(),
        method: req.method,
        url: loggedURL.toString(),
      };
      if (api === "graphql") {
        try {
          const op = JSON.parse(body.toString("utf8")).operationName;
          if (op) record.graphql_operation = op;
        } catch {
          // Not JSON — log the request without an operation name.
        }
      }
      // Each request is logged exactly once: when its response finishes or
      // fails, or — if bk exits first, e.g. cancelling sibling downloads after
      // an error — as cancelled when the invocation is written (see pending).
      let status;
      let bytes = 0;
      let done = false;
      const logOnce = (extra) => {
        if (done) return;
        done = true;
        pending.delete(logOnce);
        requests++;
        writeLog({ ...record, status, duration_ms: Date.now() - started,
                   bytes: status === undefined ? undefined : bytes, ...extra });
      };
      pending.add(logOnce);
      const headers = { ...req.headers, host: target.host };
      const rewrite = rewriteDownloadURLs && api === "rest";
      // Ask for a plain body, so a JSON response can be rewritten.
      if (rewrite) delete headers["accept-encoding"];
      const client = target.protocol === "http:" ? http : https;
      const hostname = target.hostname.replace(/^\[|\]$/g, "");
      const ipVersion = isIP(hostname);
      const port = target.port || (target.protocol === "https:" ? "443" : "80");
      const bypass = hostname === "localhost"
        || (ipVersion && bypassIPs.check(hostname, ipVersion === 4 ? "ipv4" : "ipv6"))
        || bypassHosts.some((rule) => (!rule.port || rule.port === port)
          && (rule.host === "*" || hostname === rule.host
            || (!ipVersion && hostname.endsWith(rule.host.startsWith(".") ? rule.host : `.${rule.host}`))));
      const upstreamReq = client.request(target, { method: req.method, headers, agent: bypass ? false : agents[target.protocol] }, (upstreamRes) => {
        status = upstreamRes.statusCode;
        // The connection can drop after the headers arrived: the body then
        // ends with "close" (and possibly "error") but no "end". Log the
        // request as failed and cut bk's connection too, so bk fails the
        // way it would have against the API directly instead of waiting
        // forever.
        const fail = () => {
          logOnce({ error: "response interrupted" });
          res.destroy();
        };
        upstreamRes.on("error", fail);
        upstreamRes.on("close", () => { if (!done) fail(); });
        const isJSON = String(upstreamRes.headers["content-type"] || "").includes("json");
        if (rewrite && isJSON && !upstreamRes.headers["content-encoding"]) {
          const parts = [];
          upstreamRes.on("data", (c) => { bytes += c.length; parts.push(c); });
          upstreamRes.on("end", () => {
            const out = rewriteDownloadURLsIn(Buffer.concat(parts));
            const outHeaders = { ...upstreamRes.headers, "content-length": String(out.length) };
            delete outHeaders["transfer-encoding"];
            logOnce({});
            res.writeHead(upstreamRes.statusCode ?? 502, outHeaders);
            res.end(out);
          });
          return;
        }
        res.writeHead(upstreamRes.statusCode ?? 502, upstreamRes.headers);
        upstreamRes.on("data", (c) => { bytes += c.length; res.write(c); });
        upstreamRes.on("end", () => {
          logOnce({});
          res.end();
        });
      });
      upstreamReq.on("error", (err) => {
        // Transport messages may contain a credential-bearing URL or proxy.
        logOnce({ error: err.code || "upstream request failed" });
        res.writeHead(502, { "content-type": "text/plain" });
        res.end("bk shim: upstream request failed\n");
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
      for (const finish of [...pending]) finish({ error: "cancelled: bk exited before the response finished" });
      writeLog({ type: "invocation", invocation, argv: loggedArgv, exit_code: exitCode, duration_ms: durationMs, requests });
    });
  });
}
