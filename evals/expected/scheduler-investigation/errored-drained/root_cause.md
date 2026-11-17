# Drained errored work was not completed

At 2026-11-17T03:00:00Z the core pool is errored and drained, but all 7 attempts are still waiting. None completed or passed, and there are no active or expired leases. Pool detail returns `Pool errored after partial population`.

Explain that the pool errored and is drained but some work was not completed. Neither drained nor zero leases establishes that all tests ran. Distinguish the returned error from an inferred deeper cause. Reject calling this finished, passed execution or treating waiting attempts as completed/canceled. No fix is requested.
