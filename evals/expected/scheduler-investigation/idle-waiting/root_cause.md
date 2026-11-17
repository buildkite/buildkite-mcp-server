# Zero active leases does not establish completion

At 2026-11-17T03:00:00Z the single core pool is consuming and not drained. All 7 attempts are waiting, none leased or completed, with zero active and expired leases and zero recorded results. Waiting estimated p50 workload is 420,000 ms; the oldest waiting attempt is ten minutes old at the observation time.

Say testing is not finished: waiting work remains despite no active leases. The aggregate data cannot establish why workers are not leasing work. Reject declaring finished/passed execution or a proven infrastructure cause. No code fix is requested.
