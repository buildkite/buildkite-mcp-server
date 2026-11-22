# Unfinished pool on a later discovery page

Discovery returns at most two pools per page even with per_page 100. The first page's core and features pools are consumed, drained and all-passed, with 7 and 11 completed/passed attempts respectively. A next cursor leads to browser. With per_page 1 there are three pages instead; follow all continuation links while preserving pipeline/build filters, page size and the opaque cursor (including its literal plus sign).

Browser is consuming, not drained: 5 entries/attempts, 2 waiting, 1 leased, 2 completed, 0 canceled; results 2 passed, 0 failed/errored. One active lease holds one attempt; no expired leases. Metrics are observed at `2026-11-22T20:10:00Z`.

The correct build-wide conclusion is not all finished or all passed: browser still has outstanding work. The build's passed state and first page's successful pools are insufficient. Full investigation credit requires following discovery's continuation(s) to enumerate the complete set and reading metrics for every pool, including browser. Resolving metadata pool IDs alone does not demonstrate correct cursor handling. A short first page is not the last page when links.next exists. Do not deduplicate pools by shared suite ID, infer unique-test totals from attempts or claim a stall from one snapshot. No changes are requested.
