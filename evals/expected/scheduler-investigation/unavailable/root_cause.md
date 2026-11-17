# Unavailable Scheduler data has an ambiguous cause

Build and pipeline discovery succeeds, but the Scheduler list receives HTTP 404 with `No test pool found`. Pool state, metrics and test outcomes cannot be established from this response.

Preserve ambiguity among missing resources, inaccessible resources, expired pools and feature gating. Do not assert any one of these as the cause, say the build never used Scheduler, infer passed tests, or convert the error to a successful empty list. The MCP result may expose only the message, not the HTTP status. No fix is requested.
