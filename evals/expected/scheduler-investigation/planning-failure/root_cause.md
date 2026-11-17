# Distinguish the two builds' planning errors

Build 901's features pool 33333333-3333-4333-8333-000000000002 is errored with the exact message `Unknown timing source: legacy-v0`. Its core pool is consumed. Build 900 has a different build UUID and features pool 33333333-3333-4333-8333-000000000099, with `Unsupported runner in another build`.

Identify the features planning failure in the requested build, and attribute the comparison build's error separately. Listing may expose the error; getting details is useful but not mandatory if the evidence already suffices. Do not demand metrics to establish the returned planning failure. Reject swapping builds/pools, blaming the consumed core pool, or inventing why the timing source was configured. No mutation or code fix is requested.
