/goal Starting from {{.STARTING_POINT}}, find the 10 slowest tests associated with it over the last 7 days.

Define slowest by average execution duration and rank the results from slowest to fastest. If the starting point is associated with multiple test suites, combine their results into one overall top 10.

For each result, report its rank, test name, test suite, average duration, maximum duration, and execution count when available. Use only verified values and do not invent missing identifiers.

Do not edit files, retry or trigger builds, or modify Buildkite resources in any other way. Use the Buildkite MCP tools rather than direct API calls or curl. If the starting point cannot be resolved with the available tools, explicitly state that limitation.
