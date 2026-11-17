# All Scheduler pools finished and passed

At 2026-11-17T03:00:00Z, build 901 has three pools: core has 11 completed/passed attempts, features 13, and browser 17. All 41 attempts completed and passed. Every pool is consumed and drained, with zero waiting, leased or canceled attempts, no failed/errored results, and no active or expired leases.

A correct investigation discovers the build and pipeline UUIDs, lists the target build's pools and checks every pool's metrics. It concludes that all Scheduler-managed attempts finished and passed as of the snapshot, not that every possible test or the entire build passed. The separate build state is still running. Successful API access is also demonstrated by this case; do not provision a duplicate positive-access scenario.

Reject completion based only on drained or zero active leases, a build-wide Scheduler conclusion from one pool, and treating attempt counts as necessarily unique-test counts. Do not require any specific wording or a code fix.
