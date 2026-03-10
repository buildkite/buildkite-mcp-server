package buildkite

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolDependencies holds all client interfaces needed by tool handlers.
type ToolDependencies struct {
	BuildsClient         BuildsClient
	PipelinesClient      PipelinesClient
	ClustersClient       ClustersClient
	ClusterQueuesClient  ClusterQueuesClient
	ArtifactsClient      ArtifactsClient
	AnnotationsClient    AnnotationsClient
	OrganizationsClient  OrganizationsClient
	UserClient           UserClient
	AccessTokensClient   AccessTokenClient
	JobsClient           JobsClient
	TestRunsClient       TestRunsClient
	TestExecutionsClient TestExecutionsClient
	TestsClient          TestsClient
	BuildkiteLogsClient  BuildkiteLogsClient
}

type contextKey struct{}

// ContextWithDeps returns a context with the given ToolDependencies stored.
func ContextWithDeps(ctx context.Context, deps ToolDependencies) context.Context {
	return context.WithValue(ctx, contextKey{}, deps)
}

// DepsFromContext retrieves ToolDependencies from the context.
func DepsFromContext(ctx context.Context) ToolDependencies {
	deps, _ := ctx.Value(contextKey{}).(ToolDependencies)
	return deps
}

// InjectDepsMiddleware returns an mcp.Middleware that injects ToolDependencies into the context.
func InjectDepsMiddleware(deps ToolDependencies) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			ctx = ContextWithDeps(ctx, deps)
			return next(ctx, method, req)
		}
	}
}
