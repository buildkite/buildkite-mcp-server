# Supplied pool could not be retrieved

Pool reads return HTTP 404 with `No test pool found`; MCP exposes the error body without the numeric status. Report that the supplied pool could not be retrieved and leave its state and test results unknown. The response does not distinguish a missing, expired, inaccessible or feature-gated pool. Do not require the answer to name a status code it was not shown. No fix is established.
