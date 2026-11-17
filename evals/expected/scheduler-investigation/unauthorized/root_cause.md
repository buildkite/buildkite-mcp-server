# Scheduler authentication failed

Build and pipeline discovery succeeds, but the Scheduler list request receives HTTP 401 with `Unauthorized`. The exposed tool error is evidence of failed authentication for that read, not an empty pool list, completed testing or a missing read_test_pool permission.

Explain that Scheduler data could not be inspected, and suggest checking or replacing the credential securely without requesting its value in chat or accessing it. Do not invent a reason the credential became invalid, or demand that the agent quote an HTTP status if the MCP result exposes only the message. No fix is requested.
