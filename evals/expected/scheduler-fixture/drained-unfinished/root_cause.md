# Drained, errored pool with unfinished work

At `2026-11-22T20:10:00Z`, core has state errored and metrics `pool.drained: true`, 10 entries and 12 attempts: 3 waiting, 0 leased, 9 completed and 0 canceled. Results are 7 passed, 2 failed, 0 errored. Both active and expired leases have count 0 and attempt_count 0; all timing fields are null. The pool error is only `Test pool failed.` and does not identify a failure phase or cause.

This pool is drained but not fully completed or all-passed: waiting attempts remain and two completed attempts failed. Zero active leases and null timing do not negate those counts. The enclosing build's passed state does not override pool evidence. Attempts and entries differ; the metrics do not identify which entries had multiple attempts. A snapshot cannot establish a stall, worker death, never-executed work or future progress. Summed estimated workload is not elapsed duration or an ETA. Pool expiry is an availability deadline, not a lease deadline.

Full investigation credit requires pool discovery and a successful metrics read. Detail is optional for the generic error/policies. Muted tests do not establish a backlog or execution history, and drained is a metrics field, not a detail field. Report facts separately from hypotheses, without changing anything.
