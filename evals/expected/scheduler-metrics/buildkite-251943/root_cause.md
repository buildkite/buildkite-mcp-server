# Scheduler metrics review

Build https://buildkite.com/buildkite/buildkite/builds/251943 used Test Scheduler: its metadata records core, features and browser pool IDs. The task is a read-only review of the metrics behind Orchestration, not a browser UI test or a failure diagnosis.

A successful investigation discovers the build and pipeline UUIDs, lists the build's pools (following continuation links if present), and retrieves metrics for each pool. The answer identifies the pools and accurately summarizes the returned state, entry/attempt counts, result counts and lease activity at their observed_at timestamps. Numerical summaries must agree with those responses. Do not infer passing tests from the build's passed state or a pool's consumed/drained state alone, or execution history and stalls from a single snapshot.

The live data is not a fixed snapshot: use the responses captured in this run rather than invented expected totals. A denied, empty, expired or otherwise unavailable read does not demonstrate this successful-access scenario, even if the agent honestly reports the limitation. Full credit for conclusion and investigation requires successful metrics reads and an accurate, evidence-backed review. No fix is requested. The eval does not verify organization membership, flag administration or browser rendering.
