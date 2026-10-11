// Command tool-token-report prints an approximate token count for the
// server's MCP tool definitions as Markdown and/or JSON.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/alecthomas/kong"
	"github.com/buildkite/buildkite-mcp-server/internal/tokenreport"
)

const stdoutPath = "-"

type cliArgs struct {
	Markdown      string `help:"Write the Markdown report to this path. Use '-' for stdout." placeholder:"PATH"`
	JSON          string `help:"Write the JSON report to this path. Use '-' for stdout." placeholder:"PATH"`
	MaxToolTokens int    `help:"Fail with exit code 2 if any tool exceeds this many tokens. 0 disables the check." default:"0"`
}

// Validate implements kong.Validatable.
func (c *cliArgs) Validate() error {
	if c.Markdown == stdoutPath && c.JSON == stdoutPath {
		return errors.New("only one of --markdown and --json may write to stdout ('-')")
	}
	if c.Markdown != "" && c.Markdown == c.JSON {
		return errors.New("--markdown and --json must write to different paths")
	}
	if c.MaxToolTokens < 0 {
		return errors.New("--max-tool-tokens must not be negative")
	}
	return nil
}

func main() {
	var cli cliArgs
	kong.Parse(&cli,
		kong.Name("tool-token-report"),
		kong.Description("Report approximate token usage of the MCP tool definitions."),
		kong.UsageOnError(),
	)

	code, err := run(cli, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
	os.Exit(code)
}

// run writes the requested reports and returns the process exit code: 0 on
// success, 1 on error, 2 when a tool exceeds the ceiling. Outputs are always
// written before the ceiling result is returned. Invalid flags are rejected
// earlier by kong, which exits 80.
func run(cli cliArgs, stdout io.Writer) (int, error) {
	if cli.Markdown == "" && cli.JSON == "" {
		cli.Markdown = stdoutPath
	}

	report, err := tokenreport.Build(cli.MaxToolTokens)
	if err != nil {
		return 1, err
	}

	if cli.Markdown != "" {
		if err := writeTo(cli.Markdown, stdout, func(w io.Writer) error { return tokenreport.WriteMarkdown(w, report) }); err != nil {
			return 1, err
		}
	}
	if cli.JSON != "" {
		if err := writeTo(cli.JSON, stdout, func(w io.Writer) error { return tokenreport.WriteJSON(w, report) }); err != nil {
			return 1, err
		}
	}

	if len(report.Violations) > 0 {
		return 2, fmt.Errorf("%d tool(s) exceed %d tokens: %v", len(report.Violations), cli.MaxToolTokens, report.Violations)
	}
	return 0, nil
}

func writeTo(path string, stdout io.Writer, write func(io.Writer) error) (err error) {
	if path == stdoutPath {
		return write(stdout)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	return write(f)
}
