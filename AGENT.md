# AGENT.md - Buildkite MCP Server

## Build/Test Commands
- `make build` - Build the binary
- `make test` - Run all tests with coverage
- `go test ./internal/buildkite/...` - Run tests for specific package
- `go test -run TestName` - Run single test by name
- `make lint` - Run golangci-lint
- `make check` - Run linting and tests
- `make all` - Full build pipeline

## Architecture
- **Main binary**: `cmd/buildkite-mcp-server/main.go` - MCP server for Buildkite API access
- **Core packages**: `internal/buildkite/` - API wrappers, `internal/commands/` - CLI commands
- **Key dependencies**: `github.com/mark3labs/mcp-go` (MCP protocol), `github.com/buildkite/go-buildkite/v4` (API client)
- **Configuration**: Environment variables (BUILDKITE_API_TOKEN, OTEL tracing)

## Code Style
- Use `zerolog` for logging, `testify/require` for tests
- Mock interfaces for testing (see `MockPipelinesClient` pattern)
- Import groups: stdlib, external, internal (`github.com/buildkite/buildkite-mcp-server/internal/...`)
- Error handling: return errors up the stack, log at top level
- Package names: lowercase, descriptive (buildkite, commands, trace, tokens)
- Use contexts for cancellation and tracing throughout
