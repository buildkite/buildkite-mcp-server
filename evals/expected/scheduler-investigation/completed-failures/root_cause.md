# Finished execution is not all-passed execution

At 2026-11-17T03:00:00Z the single core pool has 19 attempts, all completed: 16 passed, 2 failed and 1 errored. It is consumed and drained with no waiting, leased or canceled attempts and no active or expired leases.

The correct conclusion is that Scheduler-managed execution finished, but not all attempts passed. Include the result counts and snapshot scope. Reject declaring success from consumed/drained or zero leases, saying execution is still pending solely because failures exist, or identifying particular failed tests from these aggregates. No fix is requested.

Max_attempts=1 limits policy-created attempts, not how many times a released/expired attempt may have been leased. Do not infer that each entry executed once from this policy or from entries.total=attempts.total; no recorded re-execution history is exposed.
