# Mixed readable and denied pools

The offline build lists three pools with no continuation: core (consumed), features (consumed) and browser (consuming). Core metrics show 11 entries, 11 completed attempts, 11 passed; features show 7 entries, 7 completed attempts, 7 passed. Both have zero waiting/leased/canceled attempts, zero failed/errored results, zero active/expired leases and drained true at `2026-11-22T20:10:00Z`.

Browser detail and metrics return HTTP 403 with `You do not have access to this test pool`. Its consuming state is known from the listing, but its counts, results, lease activity and drained status are unknown, not zero. Do not invent a missing-scope diagnosis: this response names resource access, not a specific absent scope. Successful reads for other pools do not establish access to browser.

A correct answer summarizes the two readable pools and explicitly preserves browser's unknown metrics. It cannot conclude that all Scheduler work finished or passed, despite the enclosing build's passed state. It must list all pools and attempt the browser metrics read to earn full investigation credit. Muted tests are a planning snapshot and do not establish execution or contribution to result totals. Attribute each claim to an actual returned field; `drained` comes from metrics, not detail. No changes are requested.
