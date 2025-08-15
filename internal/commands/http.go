package commands

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/buildkite/buildkite-mcp-server/pkg/server"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type HTTPCmd struct {
	Listen string `help:"The address to listen on." default:"localhost:3000" env:"HTTP_LISTEN_ADDR"`
	UseSSE bool   `help:"Use deprecated SSS transport instead of Streamable HTTP." default:"false"`
}

func (c *HTTPCmd) Run(ctx context.Context, globals *Globals) error {
	mcpServer := server.NewMCPServer(globals.Version, globals.Client, globals.BuildkiteLogsClient)

	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", c.Listen, err)
	}
	logEvent := log.Ctx(ctx).Info().Str("address", c.Listen)

	mux := http.NewServeMux()
	srv := newServerWithTimeouts(mux)

	if c.UseSSE {
		handler := mcpserver.NewSSEServer(mcpServer)
		mux.Handle("/sse", handler)
		logEvent.Str("transport", "sse").Str("endpoint", fmt.Sprintf("http://%s/sse", listener.Addr())).Msg("Starting SSE HTTP server")
	} else {
		handler := mcpserver.NewStreamableHTTPServer(mcpServer)
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
