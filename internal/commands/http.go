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

	factory := server.NewPerRequestServerFactory(globals.Version, deps, c.EnabledToolsets, c.ReadOnly)

	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", c.Listen, err)
	}

	mux := http.NewServeMux()
	srv := newServerWithTimeouts(mux, 30*time.Second)

	mux.HandleFunc("/health", healthHandler)

	handler := mcp.NewStreamableHTTPHandler(factory, &mcp.StreamableHTTPOptions{
		Stateless: true,
	})
	mux.Handle("/mcp", handler)

	log.Ctx(ctx).Info().
		Str("address", c.Listen).
		Str("transport", "streamable-http").
		Str("endpoint", fmt.Sprintf("http://%s/mcp", listener.Addr())).
		Msg("Starting Streamable HTTP server")

	return srv.Serve(listener)
}

func newServerWithTimeouts(mux *http.ServeMux, writeTimeout time.Duration) *http.Server {
	return &http.Server{
		Handler:           otelhttp.NewHandler(mux, "mcp-server"),
		ReadHeaderTimeout: 30 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       60 * time.Second,
	}
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
