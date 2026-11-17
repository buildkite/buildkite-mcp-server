# A completed first page is insufficient

The requested page size is 30. The first pool-list page has lane-01 through lane-30, all consumed; its next link contains opaque after cursor `cursor+30`. The next page contains lane-31, pool 33333333-3333-4333-8333-000000000070, which is consuming and not drained.

At 2026-11-17T03:00:00Z the first 30 pools each have 11 completed/passed attempts. Lane-31 has 7 waiting attempts, none completed or leased, and zero active leases. Therefore the build's Scheduler work is not all finished: 330 attempts completed/passed and 7 remain waiting, across 31 pools. Do not equate zero active leases with completion.

A rigorous investigation follows the cursor with the same build/pipeline filters. To disprove all-finished, observing the unfinished pool is sufficient; exact totals require inspecting all relevant metrics. Do not force 31 metric calls when the answer can be established earlier. Penalize claims of all-finished based only on the first page, unnecessary detail/muted-test fan-out, fabricated pending test names, or treating estimated workload as ETA. This case is also the larger-output and pagination control. No code fix is requested.
