package tokenreport

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/buildkite/buildkite-mcp-server/pkg/toolsets"
	"github.com/stretchr/testify/require"
)

func TestEstimate(t *testing.T) {
	require.Equal(t, 0, Estimate(nil))
	require.Equal(t, 1, Estimate([]byte("a")))
	require.Equal(t, 10, Estimate(make([]byte, 36)))
	require.Equal(t, 11, Estimate(make([]byte, 37)))
	require.Equal(t, 28, Estimate(make([]byte, 100)))
}

func TestBuild(t *testing.T) {
	r, err := Build(0)
	require.NoError(t, err)

	registry := toolsets.NewToolsetRegistry()
	registry.RegisterToolsets(toolsets.CreateBuiltinToolsets())
	require.Len(t, r.Tools, len(registry.GetEnabledTools([]string{"all"}, false)))
	require.Equal(t, len(r.Tools), r.Totals.All.Tools)
	require.LessOrEqual(t, r.Totals.ReadOnly.Total, r.Totals.All.Total)
	require.Positive(t, r.Instructions.Tokens)

	for _, ts := range r.Toolsets {
		builtin, ok := registry.Get(ts.Name)
		require.True(t, ok)
		require.Equal(t, len(builtin.Tools), ts.Tools)
		tokens := 0
		for _, def := range builtin.Tools {
			encoded, err := json.Marshal(def.Tool)
			require.NoError(t, err)
			tokens += Estimate(encoded)
		}
		require.Equal(t, tokens, ts.Tokens, ts.Name)
	}

	seen := make(map[string]bool)
	sum, readOnlySum := 0, 0
	for _, tool := range r.Tools {
		require.False(t, seen[tool.Name], "duplicate tool %s", tool.Name)
		seen[tool.Name] = true
		require.Positive(t, tool.Tokens, tool.Name)
		require.NotEmpty(t, tool.Toolset)
		sum += tool.Tokens
		if tool.ReadOnly {
			readOnlySum += tool.Tokens
		}
	}
	require.Equal(t, r.Totals.All.ToolTokens, sum)
	require.Equal(t, r.Totals.ReadOnly.ToolTokens, readOnlySum)
	require.Equal(t, len(registry.GetEnabledTools([]string{"all"}, true)), r.Totals.ReadOnly.Tools)
	require.Empty(t, r.Violations)
}

func TestWriteJSONRoundTrip(t *testing.T) {
	r, err := Build(1)
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, WriteJSON(&buf, r))

	var got Report
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, r, got)
}

func TestWriteMarkdown(t *testing.T) {
	r, err := Build(0)
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, WriteMarkdown(&buf, r))
	out := buf.String()

	require.Contains(t, out, "### MCP tool definition tokens (approx)")
	require.Contains(t, out, "<details>")
	for _, tool := range r.Tools {
		require.Contains(t, out, tool.Name)
	}
	require.NotContains(t, out, "❌")
	require.NotContains(t, out, "✅")
	require.NotContains(t, out, "Over ceiling")
}

func TestCeiling(t *testing.T) {
	r, err := Build(1)
	require.NoError(t, err)
	require.Len(t, r.Violations, len(r.Tools))

	var buf bytes.Buffer
	require.NoError(t, WriteMarkdown(&buf, r))
	out := buf.String()
	require.Contains(t, out, "❌")
	require.Contains(t, out, "Over ceiling")
	require.Contains(t, out, "token ceiling")

	summary := ViolationSummary(r)
	require.Contains(t, summary, "tools exceed the 1 token ceiling")
	for _, tool := range r.Tools {
		require.Contains(t, summary, tool.Name)
	}
	require.Contains(t, summary, `"tool-tokens" annotation`)

	r, err = Build(1_000_000)
	require.NoError(t, err)
	require.Empty(t, r.Violations)
	require.Empty(t, ViolationSummary(r))

	buf.Reset()
	require.NoError(t, WriteMarkdown(&buf, r))
	require.Contains(t, buf.String(), "✅ **All ")
	require.NotContains(t, buf.String(), "Over ceiling")
}

func TestNum(t *testing.T) {
	require.Equal(t, "0", num(0))
	require.Equal(t, "999", num(999))
	require.Equal(t, "2,480", num(2480))
	require.Equal(t, "1,234,567", num(1234567))
	require.True(t, strings.HasPrefix(num(-1500), "-1,"))
}
