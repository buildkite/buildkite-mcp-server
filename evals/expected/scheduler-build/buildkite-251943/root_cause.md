# Live investigation of a passing production build

At authoring time, build `buildkite/buildkite` #251943 was passed, and its metadata identified core, features and browser Scheduler pools. That build-level result alone does not establish each pool's results. Pool availability is time-limited; this reference does not assume the pools remain readable.

The answer should discover the build's pools, follow any list pagination, and support its completion/pass conclusion with each pool's live metrics. Report observed counts and observation times rather than borrowing counts from the recorded example. Pool detail can explain state or errors, but consumed/drained state alone is not passing-result evidence. Keep the conclusion scoped to Scheduler-managed work and the observations actually returned.

If access fails or pool data is no longer available, explain the observed limitation and leave unsupported pool outcomes unknown; do not infer an empty successful result from an error. No fix is requested.
