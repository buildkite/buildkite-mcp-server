Both tests must be fixed at their cause; deleting, skipping, quarantining or loosening either test, or retrying the build, is not a fix.

1. `search_records_spec.rb`: stop `soft_destroyable_spec.rb` leaking its test model, e.g. define it with `stub_const` and/or remove it from ActiveRecord's descendants after the spec, so `Buildkite::Console::Magic` never sees it. Optionally also make `magic.rb:252` skip class names that do not resolve.

2. `viewing_build_waterfall_spec.rb`: fix the drawer container's classes from the Tailwind v4 migration so only one flex-direction applies per dock position: `flex-col-reverse` when docked at the bottom (`flex-row-reverse` only for a side dock). The drawer then spans the full width and the job name shows. Removing `truncate` from the job name only hides the symptom.
