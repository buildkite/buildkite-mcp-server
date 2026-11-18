# Drained errored work was not completed

At 2026-11-17T03:00:00Z the core pool is errored and drained, but all 7 attempts are still waiting. None completed or passed, and there are no active or expired leases. Pool detail returns only `Test pool failed.`. All four timing fields are null: finalized errored/drained pools suppress waiting and lease ages even when waiting counts remain.

Explain that the pool errored and is drained but some work was not completed. Neither drained nor zero leases establishes that all tests ran. Do not infer the failure phase/cause, waiting age, uninterrupted stall or never-leased history from the generic error, counts or null timings. Distinguish the returned error from hypotheses. Scope the finding to the snapshot, not guaranteed current production state. Reject calling this finished, passed execution or treating waiting attempts as completed/canceled. No fix is requested.
