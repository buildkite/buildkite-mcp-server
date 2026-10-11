// Package tokenreport estimates how many model-context tokens the server's
// MCP tool definitions and instructions consume.
package tokenreport

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/buildkite/buildkite-mcp-server/pkg/server"
	"github.com/buildkite/buildkite-mcp-server/pkg/toolsets"
)

// CharsPerToken is the heuristic used to convert bytes to tokens. It is the
// midpoint between Claude and OpenAI token counts, measured over this server's
// 71 tool definitions (99,454 bytes of compact JSON, October 2026):
//   - Claude count_tokens (Opus/Sonnet/Haiku 5.5): 34,446 tokens, 2.89 chars/token
//   - OpenAI o200k_base: 20,589 tokens, 4.83 chars/token
const CharsPerToken = 3.6

// Estimate returns the approximate token count for b.
func Estimate(b []byte) int {
	return int(math.Ceil(float64(len(b)) / CharsPerToken))
}

// Tool is the token breakdown of a single tool definition.
type Tool struct {
	Name              string `json:"name"`
	Toolset           string `json:"toolset"`
	ReadOnly          bool   `json:"read_only"`
	Bytes             int    `json:"bytes"`
	DescriptionTokens int    `json:"description_tokens"`
	SchemaTokens      int    `json:"schema_tokens"`
	Tokens            int    `json:"tokens"`
	OverLimit         bool   `json:"over_limit,omitempty"`
}

// Toolset is the subtotal for one toolset.
type Toolset struct {
	Name   string `json:"name"`
	Tools  int    `json:"tools"`
	Tokens int    `json:"tokens"`
}

// Scenario is the total for a deployment mode (all tools or read-only).
type Scenario struct {
	Tools        int `json:"tools"`
	ToolTokens   int `json:"tool_tokens"`
	Instructions int `json:"instructions_tokens"`
	Total        int `json:"total_tokens"`
}

// Totals holds the side-by-side scenarios.
type Totals struct {
	All      Scenario `json:"all"`
	ReadOnly Scenario `json:"read_only"`
}

// Instructions holds the instruction token counts for each mode.
type Instructions struct {
	Tokens         int `json:"tokens"`
	ReadOnlyTokens int `json:"read_only_tokens"`
}

// Report is the full token report.
type Report struct {
	CharsPerToken float64      `json:"chars_per_token"`
	MaxToolTokens int          `json:"max_tool_tokens,omitempty"`
	Violations    []string     `json:"violations,omitempty"`
	Totals        Totals       `json:"totals"`
	Toolsets      []Toolset    `json:"toolsets"`
	Tools         []Tool       `json:"tools"`
	Instructions  Instructions `json:"instructions"`
}

// Build computes the report. When maxToolTokens is greater than 0, tools above
// that many tokens are flagged and listed in Violations.
func Build(maxToolTokens int) (Report, error) {
	r := Report{CharsPerToken: CharsPerToken}
	if maxToolTokens > 0 {
		r.MaxToolTokens = maxToolTokens
	}

	all := toolsets.CreateBuiltinToolsets()
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}
	slices.Sort(names)

	for _, name := range names {
		ts := Toolset{Name: name}
		for _, def := range all[name].Tools {
			full, err := json.Marshal(def.Tool)
			if err != nil {
				return Report{}, fmt.Errorf("marshal tool %s: %w", def.Tool.Name, err)
			}
			schema, err := json.Marshal(def.Tool.InputSchema)
			if err != nil {
				return Report{}, fmt.Errorf("marshal schema for %s: %w", def.Tool.Name, err)
			}
			t := Tool{
				Name:              def.Tool.Name,
				Toolset:           name,
				ReadOnly:          def.IsReadOnly(),
				Bytes:             len(full),
				DescriptionTokens: Estimate([]byte(def.Tool.Description)),
				SchemaTokens:      Estimate(schema),
				Tokens:            Estimate(full),
				OverLimit:         maxToolTokens > 0 && Estimate(full) > maxToolTokens,
			}
			r.Tools = append(r.Tools, t)

			ts.Tools++
			ts.Tokens += t.Tokens
			r.Totals.All.Tools++
			r.Totals.All.ToolTokens += t.Tokens
			if t.ReadOnly {
				r.Totals.ReadOnly.Tools++
				r.Totals.ReadOnly.ToolTokens += t.Tokens
			}
		}
		r.Toolsets = append(r.Toolsets, ts)
	}

	r.Instructions.Tokens = Estimate([]byte(server.BuildkiteServerInstructions([]string{toolsets.ToolsetAll}, false)))
	r.Instructions.ReadOnlyTokens = Estimate([]byte(server.BuildkiteServerInstructions([]string{toolsets.ToolsetAll}, true)))
	r.Totals.All.Instructions = r.Instructions.Tokens
	r.Totals.All.Total = r.Totals.All.ToolTokens + r.Instructions.Tokens
	r.Totals.ReadOnly.Instructions = r.Instructions.ReadOnlyTokens
	r.Totals.ReadOnly.Total = r.Totals.ReadOnly.ToolTokens + r.Instructions.ReadOnlyTokens

	sort.SliceStable(r.Toolsets, func(i, j int) bool { return r.Toolsets[i].Tokens > r.Toolsets[j].Tokens })
	sort.SliceStable(r.Tools, func(i, j int) bool { return r.Tools[i].Tokens > r.Tools[j].Tokens })

	for _, t := range r.Tools {
		if t.OverLimit {
			r.Violations = append(r.Violations, t.Name)
		}
	}
	return r, nil
}

// WriteJSON writes r as indented JSON.
func WriteJSON(w io.Writer, r Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteMarkdown writes r as a Markdown report suitable for a CI annotation.
func WriteMarkdown(w io.Writer, r Report) error {
	var b strings.Builder
	limited := r.MaxToolTokens > 0

	b.WriteString("### MCP tool definition tokens (approx)\n\n")

	if limited {
		if n := len(r.Violations); n > 0 {
			noun := "tools exceed"
			if n == 1 {
				noun = "tool exceeds"
			}
			fmt.Fprintf(&b, "❌ **%d %s the %s token ceiling**\n\n", n, noun, num(r.MaxToolTokens))
		} else {
			fmt.Fprintf(&b, "✅ **All %d tools within the %s token ceiling**\n\n", len(r.Tools), num(r.MaxToolTokens))
		}
	}

	b.WriteString("| Scenario | Tools | Tool tokens | Instructions | Total |\n|---|---:|---:|---:|---:|\n")
	for _, s := range []struct {
		name string
		s    Scenario
	}{{"All toolsets", r.Totals.All}, {"Read-only", r.Totals.ReadOnly}} {
		fmt.Fprintf(&b, "| %s | %d | %s | %s | **%s** |\n", s.name, s.s.Tools, num(s.s.ToolTokens), num(s.s.Instructions), num(s.s.Total))
	}
	b.WriteString("\n")

	if len(r.Violations) > 0 {
		b.WriteString("#### Over ceiling\n| | Tool | Toolset | Total | Over by |\n|---|---|---|---:|---:|\n")
		for _, t := range r.Tools {
			if t.OverLimit {
				fmt.Fprintf(&b, "| ❌ | %s | %s | %s | +%s |\n", t.Name, t.Toolset, num(t.Tokens), num(t.Tokens-r.MaxToolTokens))
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("#### By toolset\n| Toolset | Tools | Tokens | Share |\n|---|---:|---:|---:|\n")
	for _, ts := range r.Toolsets {
		share := 0.0
		if r.Totals.All.ToolTokens > 0 {
			share = float64(ts.Tokens) * 100 / float64(r.Totals.All.ToolTokens)
		}
		fmt.Fprintf(&b, "| %s | %d | %s | %.1f%% |\n", ts.Name, ts.Tools, num(ts.Tokens), share)
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "<details><summary>All tools (%d)</summary>\n\n", len(r.Tools))
	if limited {
		b.WriteString("| | Tool | Toolset | RO | Desc | Schema | Total |\n|---|---|---|:-:|---:|---:|---:|\n")
	} else {
		b.WriteString("| Tool | Toolset | RO | Desc | Schema | Total |\n|---|---|:-:|---:|---:|---:|\n")
	}
	for _, t := range r.Tools {
		ro := ""
		if t.ReadOnly {
			ro = "✓"
		}
		status := ""
		if limited {
			status = "✅"
			if t.OverLimit {
				status = "❌"
			}
			status += " | "
		}
		fmt.Fprintf(&b, "| %s%s | %s | %s | %s | %s | %s |\n", status, t.Name, t.Toolset, ro, num(t.DescriptionTokens), num(t.SchemaTokens), num(t.Tokens))
	}
	b.WriteString("\n</details>\n\n")

	fmt.Fprintf(&b, "<sub>Estimate: ceil(compact JSON bytes / %g). Midpoint of measured Claude and OpenAI o200k counts; Claude runs ~25%% higher, GPT ~25%% lower. Excludes the client's fixed tool-use system prompt.</sub>\n", r.CharsPerToken)

	_, err := io.WriteString(w, b.String())
	return err
}

// num formats n with thousands separators.
func num(n int) string {
	s := fmt.Sprint(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}
