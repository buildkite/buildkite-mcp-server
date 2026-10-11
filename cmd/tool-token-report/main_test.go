package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidate(t *testing.T) {
	require.Error(t, (&cliArgs{Markdown: "-", JSON: "-"}).Validate())
	require.NoError(t, (&cliArgs{Markdown: "-", JSON: "out.json"}).Validate())
	require.Error(t, (&cliArgs{Markdown: "out", JSON: "out"}).Validate())
	require.Error(t, (&cliArgs{MaxToolTokens: -1}).Validate())
	require.NoError(t, (&cliArgs{}).Validate())
}

func TestRunWriteFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "r.json")

	code, err := run(cliArgs{JSON: path}, &bytes.Buffer{})
	require.Error(t, err)
	require.Equal(t, 1, code)
}

func TestRunDefaultsToMarkdownStdout(t *testing.T) {
	var out bytes.Buffer
	code, err := run(cliArgs{}, &out)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "### MCP tool definition tokens")
}

func TestRunCeilingExceeded(t *testing.T) {
	dir := t.TempDir()
	md := filepath.Join(dir, "r.md")
	js := filepath.Join(dir, "r.json")

	var out bytes.Buffer
	code, err := run(cliArgs{Markdown: md, JSON: js, MaxToolTokens: 1}, &out)
	require.Error(t, err)
	require.Equal(t, 2, code)
	require.Empty(t, out.String())

	for _, p := range []string{md, js} {
		info, statErr := os.Stat(p)
		require.NoError(t, statErr)
		require.Positive(t, info.Size())
	}
}
