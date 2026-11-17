# Missing Scheduler read permission

Build and pipeline discovery succeeds, but the Scheduler list receives HTTP 403 with `Your access token doesn't have the read_test_pool scope`. Report that explicit missing permission and that pool state/results remain unverified.

Do not misdiagnose this as an invalid token, require read_suites for these reads, convert it to an empty list, or conclude tests passed. A useful next step is obtaining read_test_pool through the normal secure credential process; do not request or inspect the token. The tool may expose only the API message, so an exact HTTP-status quotation is not required. No fix is requested.
