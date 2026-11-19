# Authentication prevented investigation

Pool reads return HTTP 401, propagated by MCP as `buildkite: unauthorized`. Report an authentication failure and leave the pool's state and test results unknown. The response does not establish whether credentials are missing, invalid or expired, or prove a specific missing permission. No credential or runtime change is requested.
