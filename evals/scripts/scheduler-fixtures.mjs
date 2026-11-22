// Synthetic API responses, not production recordings or evaluator answers.
// Generate HAR inputs for the server's existing offline replay transport.
import { mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const pipelineID = "11111111-1111-4111-8111-111111111111";
export const buildID = "22222222-2222-4222-8222-222222222222";
const suiteID = "33333333-3333-4333-8333-333333333333";
const observedAt = "2026-11-22T20:10:00Z";
const expiresAt = "2026-11-23T20:00:00Z";
const api = "https://api.buildkite.com";
const uuid = (n) => `44444444-4444-4444-8444-${String(n).padStart(12, "0")}`;
const query = (params) => new URLSearchParams(Object.entries(params).sort()).toString();

function pool(key, n, { state = "consumed", drained = true, entries = 11,
  waiting = 0, leased = 0, completed = entries, canceled = 0,
  passed = completed, failed = 0, errored = 0, active = 0, expired = 0,
  activeAttempts = 0, expiredAttempts = 0, denied = false } = {}) {
  const id = uuid(n);
  const count = (value, costs) => ({ count: value, costs: { duration_p50_ms: costs } });
  return {
    detail: {
      id, suite_id: suiteID, pipeline_id: pipelineID, build_id: buildID,
      key, state, expires_at: expiresAt, created_at: "2026-11-22T20:00:00Z", parallelism: 4,
      attempt_policy: { max_attempts: 3, min_passed: 1, max_passed: 1, max_failed: 3, parallel_attempts: 1 },
      lease: { max_attempts: 10, costs: { duration_p50_ms: 6000 } },
      muted_tests: [{ name: "muted example", path: "spec/example_spec.rb", scope: null }],
      ...(state === "errored" ? { error: { message: "Test pool failed." } } : {}),
    },
    metrics: {
      observed_at: observedAt, pool: { id, state, drained, expires_at: expiresAt },
      entries: { total: entries },
      attempts: {
        total: waiting + leased + completed + canceled,
        states: { waiting: count(waiting, waiting * 6000), leased: count(leased, leased * 6000),
          completed: count(completed, completed * 10000), canceled: count(canceled, canceled * 1000) },
        results: { passed, failed, errored },
      },
      leases: { active: { count: active, attempt_count: activeAttempts },
        expired: { count: expired, attempt_count: expiredAttempts } },
      timing: {
        oldest_waiting_attempt_created_at: waiting && !drained ? "2026-11-22T20:00:00Z" : null,
        oldest_active_lease_started_at: active ? "2026-11-22T20:09:00Z" : null,
        oldest_expired_lease_started_at: expired ? "2026-11-22T20:02:00Z" : null,
        next_active_lease_expires_at: active ? "2026-11-22T20:14:00Z" : null,
      },
    },
    denied,
  };
}

export const scenarios = {
  "coded-errors": [
    { org: "fixture-disabled", status: 404,
      error: { message: "Test Scheduler is not enabled for your organization", code: "test_scheduler_not_enabled" } },
    { org: "fixture-no-usage", status: 404,
      error: { message: "No Test Scheduler usage was found for this build", code: "test_scheduler_build_usage_not_found" } },
    { org: "fixture-generic", pools: [], missingPool: uuid(9) },
    { org: "fixture-scope", status: 403,
      error: { message: "Your access token doesn't have the read_test_pool scope" } },
  ],
  "partial-access": [{ org: "scheduler-fixture", pools: [
    pool("core", 1), pool("features", 2, { entries: 7 }),
    pool("browser", 3, { state: "consuming", drained: false, denied: true }),
  ] }],
  "drained-unfinished": [{ org: "scheduler-fixture", pools: [
    pool("core", 1, { state: "errored", entries: 10, waiting: 3, completed: 9, passed: 7, failed: 2 }),
  ] }],
  pagination: [{ org: "scheduler-fixture", paginated: true, pools: [
    pool("core", 1, { entries: 7 }), pool("features", 2),
    pool("browser", 3, { state: "consuming", drained: false, entries: 5,
      waiting: 2, leased: 1, completed: 2, active: 1, activeAttempts: 1 }),
  ] }],
  "retries-leases": [{ org: "scheduler-fixture", pools: [
    pool("core", 1, { state: "consuming", drained: false, entries: 8,
      waiting: 4, leased: 3, completed: 5, canceled: 1, passed: 3, failed: 1, errored: 1,
      active: 1, activeAttempts: 1, expired: 2, expiredAttempts: 2 }),
  ] }],
};

export function makeHAR(builds) {
  const entries = [];
  function add(path, body, status = 200) {
    const text = JSON.stringify(body);
    const entry = { request: { method: "GET", url: api + path }, response: {
      status, statusText: status === 200 ? "OK" : status === 403 ? "Forbidden" : "Not Found",
      headers: [{ name: "Content-Type", value: "application/json" }],
      content: { mimeType: "application/json", size: Buffer.byteLength(text), text },
    } };
    // Replay consumes entries. Allow a few repeated reads of the same snapshot.
    for (let i = 0; i < 4; i++) entries.push(entry);
  }
  add("/v2/organizations", builds.map(({ org }) => ({ slug: org, name: org })));
  for (const build of builds) {
    const { org, pools = [] } = build;
    const root = `/v2/organizations/${org}`;
    const pipeline = { id: pipelineID, slug: "scheduler", name: "Scheduler fixture" };
    add(`${root}/pipelines/scheduler`, pipeline);
    const meta = Object.fromEntries(pools.map(({ detail }) => [`rspec-test-pool-${detail.key}-id`, detail.id]));
    if (build.missingPool) meta["rspec-test-pool-core-id"] = build.missingPool;
    add(`${root}/pipelines/scheduler/builds/101?exclude_jobs=true&exclude_pipeline=true&include_test_engine=true`, {
      id: buildID, number: 101, pipeline, state: "passed", branch: "main", commit: "fixture",
      message: "Synthetic Scheduler eval", web_url: `https://buildkite.com/${org}/scheduler/builds/101`,
      meta_data: meta, created_at: "2026-11-22T20:00:00Z", finished_at: "2026-11-22T20:09:00Z",
    });
    add(`${root}/pipelines/scheduler/builds/101/annotations?omit_body=true&page=1&per_page=100&scope=all`, []);
    const base = `${root}/test-scheduler/pools`;
    // Accept any valid page size; pagination deliberately caps pages at two
    // even when the caller requests 100, as real APIs can return short pages.
    for (let perPage = 1; perPage <= 100; perPage++) {
      const size = build.paginated ? Math.min(perPage, 2) : perPage;
      for (let offset = 0; offset === 0 || offset < pools.length; offset += size) {
        const params = { build_id: buildID, pipeline_id: pipelineID, per_page: perPage };
        if (offset) params.after = `pool+${offset}`;
        const path = `${base}?${query(params)}`;
        const links = { self: api + path };
        if (offset + size < pools.length) {
          links.next = `${api}${base}?${query({ ...params, after: `pool+${offset + size}` })}`;
        }
        add(path, build.error ?? { items: pools.slice(offset, offset + size).map((p) => p.detail), links }, build.status ?? 200);
      }
    }
    for (const p of pools) {
      for (const suffix of ["", "/metrics"]) {
        add(`${base}/${p.detail.id}${suffix}?build_id=${buildID}`,
          p.denied ? { message: "You do not have access to this test pool" } : suffix ? p.metrics : p.detail,
          p.denied ? 403 : 200);
      }
    }
    if (build.missingPool) {
      for (const suffix of ["", "/metrics"]) {
        add(`${base}/${build.missingPool}${suffix}?build_id=${buildID}`, { message: "No test pool found" }, 404);
      }
    }
  }
  return { log: { version: "1.2", creator: { name: "scheduler-fixtures", version: "1" }, entries } };
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const output = process.argv[2];
  if (!output) throw new Error("Usage: node scheduler-fixtures.mjs <output-directory>");
  mkdirSync(output, { recursive: true });
  for (const [name, builds] of Object.entries(scenarios)) {
    writeFileSync(resolve(output, `scheduler-${name}.har`), JSON.stringify(makeHAR(builds)));
  }
}
