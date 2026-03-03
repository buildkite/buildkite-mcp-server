# AGENT.md - Buildkite MCP Server

## Build/Test Commands
- `make build` - Build the binary
- `make test` - Run all tests with coverage
- `go test ./pkg/buildkite/...` - Run tests for specific package
- `go test -run TestName` - Run single test by name
- `make lint` - Run golangci-lint
- `make lint-fix` - Run golangci-lint with auto-fix
- `make check` - Run linting and tests
- `make all` - Full build pipeline

## Architecture
- **Main binary**: `cmd/buildkite-mcp-server/main.go` - MCP server for Buildkite API access
- **Core packages**: `pkg/buildkite/` - API wrappers and tool handlers, `internal/commands/` - CLI commands, `pkg/toolsets/` - tool registry, `pkg/server/` - MCP server setup
- **Key dependencies**: `github.com/modelcontextprotocol/go-sdk` (MCP protocol), `github.com/buildkite/go-buildkite/v4` (API client)
- **Configuration**: Environment variables (BUILDKITE_API_TOKEN, OTEL tracing)
- **CI/CD**: `buildkite` organization, `buildkite-mcp-server` pipeline slug for build and test (`.buildkite/pipeline.yml`), `buildkite-mcp-server-release` pipeline slug for releases (`.buildkite/pipeline.release.yml`)

## Code Style
- Use `zerolog` for logging, `testify/require` for tests
- Mock interfaces for testing (see `MockPipelinesClient` pattern)
- Import groups: stdlib, external, internal (`github.com/buildkite/buildkite-mcp-server/internal/...`)
- Error handling: return errors up the stack, log at top level
- Package names: lowercase, descriptive (buildkite, commands, trace, tokens)
- Use contexts for cancellation and tracing throughout
- Use `utils.NewToolResultError` or `utils.NewToolResultErrorFromErr` to handle errors in tools.
- Tool handlers use typed pattern: `func(ctx, *mcp.CallToolRequest, Args) (*mcp.CallToolResult, any, error)`
- Dependencies injected via context middleware, accessed with `DepsFromContext(ctx)`