package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	gobuildkite "github.com/buildkite/go-buildkite/v4"
)

type ToolsCmd struct{}

func (c *ToolsCmd) Run(ctx context.Context, globals *Globals) error {

	client := &gobuildkite.Client{}

	// Collect all tools
	tools := BuildkiteTools(ctx, client)

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
