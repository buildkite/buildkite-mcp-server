package commands

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	"github.com/buildkite/buildkite-mcp-server/pkg/server"
	"github.com/buildkite/buildkite-mcp-server/pkg/toolsets"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type HTTPCmd struct {
	Listen          string   `help:"The address to listen on." default:"localhost:3000" env:"HTTP_LISTEN_ADDR"`
	UseSSE          bool     `help:"Use deprecated SSE transport instead of Streamable HTTP." default:"false"`
	EnabledToolsets []string `help:"Comma-separated list of toolsets to enable (e.g., 'pipelines,builds,clusters'). Use 'all' to enable all toolsets." default:"all" env:"BUILDKITE_TOOLSETS"`
	ReadOnly        bool     `help:"Enable read-only mode, which filters out write operations from all toolsets." default:"false" env:"BUILDKITE_READ_ONLY"`
}

func (c *HTTPCmd) Run(ctx context.Context, globals *Globals) error {
	if err := toolsets.ValidateToolsets(c.EnabledToolsets); err != nil {
		return err
	}

	deps := buildkite.ToolDependencies{
		BuildsClient:         globals.Client.Builds,
		PipelinesClient:      globals.Client.Pipelines,
		ClustersClient:       globals.Client.Clusters,
		ClusterQueuesClient:  globals.Client.ClusterQueues,
		ArtifactsClient:      &buildkite.BuildkiteClientAdapter{Client: globals.Client},
		AnnotationsClient:    globals.Client.Annotations,
		OrganizationsClient:  globals.Client.Organizations,
		UserClient:           globals.Client.User,
		AccessTokensClient:   globals.Client.AccessTokens,
		JobsClient:           globals.Client.Jobs,
		TestRunsClient:       globals.Client.TestRuns,
		TestExecutionsClient: globals.Client.TestRuns,
		TestsClient:          globals.Client.Tests,
		BuildkiteLogsClient:  globals.BuildkiteLogsClient,
	}

	mcpServer := server.NewMCPServer(globals.Version, deps,
		server.WithReadOnly(c.ReadOnly),
		server.WithToolsets(c.EnabledToolsets...))

	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", c.Listen, err)
	}
	logEvent := log.Ctx(ctx).Info().Str("address", c.Listen)

	mux := http.NewServeMux()
	srv := newServerWithTimeouts(mux)

	mux.HandleFunc("/health", healthHandler)

	if c.UseSSE {
		handler := mcp.NewSSEHandler(func(_ *http.Request) *mcp.Server { return mcpServer }, nil)
		mux.Handle("/sse", handler)
		logEvent.Str("transport", "sse").Str("endpoint", fmt.Sprintf("http://%s/sse", listener.Addr())).Msg("Starting SSE HTTP server")
	} else {
		handler := mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server { return mcpServer }, nil)
		mux.Handle("/mcp", handler)
		logEvent.Str("transport", "streamable-http").Str("endpoint", fmt.Sprintf("http://%s/mcp", listener.Addr())).Msg("Starting Streamable HTTP server")
	}

	return srv.Serve(listener)
}

func newServerWithTimeouts(mux *http.ServeMux) *http.Server {
	return &http.Server{
		Handler:           otelhttp.NewHandler(mux, "mcp-server"),
		ReadHeaderTimeout: 30 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
