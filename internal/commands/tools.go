package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/buildkite/buildkite-mcp-server/internal/toolsets"
	"github.com/buildkite/buildkite-mcp-server/pkg/server"
	gobuildkite "github.com/buildkite/go-buildkite/v4"
)

type ToolsCmd struct {
	EnabledToolsets []string `help:"Comma-separated list of toolsets to enable (e.g., 'pipelines,builds,clusters'). Use 'all' to enable all toolsets." default:"all" env:"BUILDKITE_TOOLSETS"`
	ReadOnly        bool     `help:"Enable read-only mode, which filters out write operations from all toolsets." default:"false" env:"BUILDKITE_READ_ONLY"`
}

func (c *ToolsCmd) Run(ctx context.Context, globals *Globals) error {
	// Validate the enabled toolsets
	if err := toolsets.ValidateToolsets(c.EnabledToolsets); err != nil {
		return err
	}

	client := &gobuildkite.Client{}

	// Collect tools with specified configuration (pass nil for ParquetClient since this is just for listing)
	tools := server.BuildkiteTools(client, nil,
		server.WithReadOnly(c.ReadOnly),
		server.WithToolsets(c.EnabledToolsets...))

	for _, tool := range tools {

		buf := new(bytes.Buffer)

		err := json.NewEncoder(buf).Encode(&tool.Tool)
		if err != nil {
			return err
		}

		fmt.Print(buf.String())

	}

	return nil
}
