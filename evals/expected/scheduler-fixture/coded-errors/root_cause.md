# Explicit Scheduler errors and ambiguous-error control

These are synthetic offline responses, not evidence about deployed production behavior. Review all four builds independently. Discovery reads succeed; listing differs by organization:

- `fixture-disabled`: HTTP 404 with `code: test_scheduler_not_enabled` and message `Test Scheduler is not enabled for your organization`. Scheduler is disabled for that organization. This does not establish possession of `read_test_pool`, because the feature check can precede the scope check.
- `fixture-no-usage`: HTTP 404 with `code: test_scheduler_build_usage_not_found` and message `No Test Scheduler usage was found for this build`. No Scheduler usage was found for this build; this does not mean pools expired.
- `fixture-generic`: HTTP 200 listing with `items: []`; metadata records a core pool ID ending `000000000009`. Detail and metrics for it return HTTP 404 with only `{"message":"No test pool found"}`. No accessible, unexpired pools were listed; pool metrics are unavailable. The specific cause remains unknown. Neither the build's age nor absence of a recognized code proves expiry or rules out resource access restrictions/gating. Code descriptions do not establish deployment support for those codes.
- `fixture-scope`: HTTP 403 with `Your access token doesn't have the read_test_pool scope`. This is a missing-scope error, not expired pools, no usage, disabled Scheduler or successful empty data.

All per-pool counts, results and lease activity are unknown, not zero. A passed enclosing build is not proof that Scheduler-managed attempts passed. Full investigation credit requires observing the actual distinguishing responses for all four builds and investigating the known generic-case pool ID. Report only calls completed and fields actually returned. No credentials, flags, pools or builds should be changed.
