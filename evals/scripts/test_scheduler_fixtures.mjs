// Deterministic integration checks only: no model, credentials or live API.
// CI supplies a freshly built production MCP binary via MCP_TEST_BINARY.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createInterface } from "node:readline";
import test from "node:test";
import { buildID, makeHAR, pipelineID, scenarios } from "./scheduler-fixtures.mjs";

async function connect(t, scenario) {
  const dir = mkdtempSync(join(tmpdir(), "scheduler-replay-"));
  const path = join(dir, "fixture.har");
  writeFileSync(path, JSON.stringify(makeHAR(scenarios[scenario])));
  assert.ok(process.env.MCP_TEST_BINARY, "MCP_TEST_BINARY must name the built server");
  const child = spawn(process.env.MCP_TEST_BINARY, ["stdio", "--read-only"], {
    env: { PATH: process.env.PATH, HOME: dir, BUILDKITE_REPLAY: path,
      // Replay ignores host. Even an accidental live fallback cannot reach production.
      BUILDKITE_BASE_URL: "http://127.0.0.1:1/" },
  });
  let stderr = "";
  child.stderr.on("data", (data) => { stderr += data; });
  const pending = new Map();
  const lines = createInterface({ input: child.stdout });
  lines.on("line", (line) => {
    const message = JSON.parse(line);
    if (pending.has(message.id)) {
      const { resolve, reject } = pending.get(message.id);
      pending.delete(message.id);
      if (message.error) reject(new Error(JSON.stringify(message.error)));
      else resolve(message.result);
    }
  });
  const fail = (error) => { for (const p of pending.values()) p.reject(error); };
  child.on("error", fail);
  child.on("exit", () => fail(new Error(`MCP server exited: ${stderr}`)));
  t.after(() => { child.kill(); lines.close(); rmSync(dir, { recursive: true, force: true }); });
  let id = 0;
  function request(method, params) {
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error(`MCP timeout: ${stderr}`)), 10000);
      const key = ++id;
      pending.set(key, {
        resolve: (value) => { clearTimeout(timer); resolve(value); },
        reject: (error) => { clearTimeout(timer); reject(error); },
      });
      child.stdin.write(JSON.stringify({ jsonrpc: "2.0", id: key, method, params }) + "\n");
    });
  }
  await request("initialize", { protocolVersion: "2025-06-18", capabilities: {},
    clientInfo: { name: "scheduler-fixture-check", version: "1" } });
  child.stdin.write(JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized" }) + "\n");
  return async (name, args, error = false) => {
    const result = await request("tools/call", { name, arguments: args });
    assert.equal(Boolean(result.isError), error, JSON.stringify(result));
    const text = result.content[0].text;
    // API errors preserve JSON bodies; transport failures are plain text.
    return text.startsWith("{") ? JSON.parse(text) : text;
  };
}

const discovery = (org = "scheduler-fixture") => ({ org_slug: org, pipeline_uuid: pipelineID, build_uuid: buildID });
const detail = (id, org = "scheduler-fixture") => ({ org_slug: org, pool_uuid: id, build_uuid: buildID });

test("coded errors and generic/missing-scope controls survive real MCP and SDK", { timeout: 30000 }, async (t) => {
  const call = await connect(t, "coded-errors");
  for (const [org, expected] of [
    ["fixture-disabled", { message: "Test Scheduler is not enabled for your organization", code: "test_scheduler_not_enabled" }],
    ["fixture-no-usage", { message: "No Test Scheduler usage was found for this build", code: "test_scheduler_build_usage_not_found" }],
    ["fixture-scope", { message: "Your access token doesn't have the read_test_pool scope" }],
  ]) assert.deepEqual(await call("list_test_pools", discovery(org), true), expected);
  const org = "fixture-generic";
  assert.deepEqual((await call("list_test_pools", discovery(org))).items, []);
  assert.deepEqual(await call("get_test_pool_metrics", detail("44444444-4444-4444-8444-000000000009", org), true), { message: "No test pool found" });
});

for (const scenario of ["partial-access", "drained-unfinished", "pagination", "retries-leases"]) {
  test(`${scenario}: discovery, detail and metrics through real MCP and SDK`, { timeout: 30000 }, async (t) => {
    const call = await connect(t, scenario);
    const build = await call("get_build", { org_slug: "scheduler-fixture", pipeline_slug: "scheduler", build_number: "101" });
    assert.equal(build.id, buildID);
    assert.equal((await call("get_pipeline", { org_slug: "scheduler-fixture", pipeline_slug: "scheduler" })).id, pipelineID);
    const first = await call("list_test_pools", { ...discovery(), per_page: 100 });
    const items = [...first.items];
    if (scenario === "pagination") {
      assert.deepEqual(items.map((p) => p.key), ["core", "features"]);
      const next = new URL(first.links.next);
      assert.equal(next.searchParams.get("after"), "pool+2");
      const second = await call("list_test_pools", { ...discovery(), per_page: 100, after: next.searchParams.get("after") });
      assert.equal(second.links.next, undefined);
      items.push(...second.items);
    }
    const metrics = [];
    for (const item of items) {
      const denied = scenario === "partial-access" && item.key === "browser";
      const pool = await call("get_test_pool", detail(item.id), denied);
      const metric = await call("get_test_pool_metrics", detail(item.id), denied);
      if (denied) {
        assert.deepEqual(metric, { message: "You do not have access to this test pool" });
        continue;
      }
      assert.equal(pool.drained, undefined);
      assert.equal(metric.observed_at, "2026-11-22T20:10:00Z");
      metrics.push(metric);
    }
    if (scenario === "partial-access") {
      assert.equal(items.length, 3);
      assert.deepEqual(metrics.map((m) => m.attempts.results.passed), [11, 7]);
    } else if (scenario === "drained-unfinished") {
      assert.equal(metrics[0].pool.drained, true);
      assert.equal(metrics[0].attempts.states.waiting.count, 3);
      assert.equal(metrics[0].attempts.results.failed, 2);
      assert.equal(metrics[0].leases.active.count, 0);
    } else if (scenario === "pagination") {
      assert.deepEqual(items.map((p) => p.key), ["core", "features", "browser"]);
      assert.equal(metrics[2].pool.drained, false);
      assert.equal(metrics[2].attempts.states.waiting.count, 2);
    } else {
      assert.equal(metrics[0].entries.total, 8);
      assert.equal(metrics[0].attempts.total, 13);
      assert.equal(metrics[0].attempts.states.leased.count, 3);
      assert.deepEqual(metrics[0].leases, { active: { count: 1, attempt_count: 1 }, expired: { count: 2, attempt_count: 2 } });
    }
    // An unrecorded request must fail locally instead of reaching a real API.
    const missing = await call("get_test_pool_metrics", detail("55555555-5555-4555-8555-555555555555"), true);
    assert.match(JSON.stringify(missing), /replay: no recorded entry/);
  });
}
