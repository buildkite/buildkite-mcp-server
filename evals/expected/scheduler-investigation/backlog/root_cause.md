# Backlog is observed, infrastructure cause is not

At 2026-11-17T03:00:00Z the core pool is consuming and not drained. Of 37 attempts, 11 are waiting, 3 leased and 23 completed/passed; none failed, errored or canceled. Two active leases cover 3 attempts. Two expired leases cover 5 attempts; these are historical lease counts, not 5 extra currently pending attempts.

Waiting estimated p50 workload is 660,000 ms, leased workload 180,000 ms. The oldest waiting attempt was created at 02:50:00Z, ten minutes before the snapshot. An active lease started at 02:58:30Z; the next expiry is 03:01:30Z. The oldest expired lease started at 02:55:00Z.

Explain that testing is unfinished because waiting and leased work remains. Expired leases are an observed symptom, not proof of worker death, agent starvation or a specific infrastructure failure. Aggregate workload is neither elapsed time nor an ETA. Scope the answer to the returned snapshot; replica lag is possible. Muted tests are unrelated to the waiting backlog. No code fix or further production action is required.
