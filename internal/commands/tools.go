package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/buildkite/buildkite-mcp-server/pkg/toolsets"
)

type ToolsCmd struct{}

func (c *ToolsCmd) Run(ctx context.Context, globals *Globals) error {
	registry := toolsets.NewToolsetRegistry()
	registry.RegisterToolsets(toolsets.CreateBuiltinToolsets())

	tools := registry.GetEnabledTools([]string{"all"}, false)

	for _, toolDef := range tools {
		buf := new(bytes.Buffer)

		err := json.NewEncoder(buf).Encode(&toolDef.Tool)
		if err != nil {
			return err
		}

		fmt.Print(buf.String())
	}

	return nil
}
