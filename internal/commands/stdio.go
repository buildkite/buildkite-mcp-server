package commands

import (
	"context"

	"github.com/mark3labs/mcp-go/server"
)

type StdioCmd struct{}

func (c *StdioCmd) Run(ctx context.Context, globals *Globals) error {

	s := NewMCPServer(ctx, globals)

	return server.ServeStdio(s)
}
