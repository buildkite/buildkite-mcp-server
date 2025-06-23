package commands

import (
	"context"

	"github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog/log"
)

type HTTPCmd struct {
	Listen     string `help:"The address to listen on." default:"localhost:3000"`
	Streamable bool   `help:"Use streamable HTTP transport instead of SSE." default:"false"`
}

func (c *HTTPCmd) Run(ctx context.Context, globals *Globals) error {

	mcpServer := NewMCPServer(ctx, globals)

	if c.Streamable {
		// Use StreamableHTTPServer
		httpServer := server.NewStreamableHTTPServer(mcpServer)

		log.Ctx(ctx).Info().Str("address", c.Listen).Bool("streamable", true).Msg("Starting streamable HTTP server")

		return httpServer.Start(c.Listen)
	} else {
		// Use SSEServer (existing behavior)
		httpServer := server.NewSSEServer(mcpServer)

		log.Ctx(ctx).Info().Str("address", c.Listen).Bool("streamable", false).Msg("Starting HTTP server")

		return httpServer.Start(c.Listen)
	}
}
