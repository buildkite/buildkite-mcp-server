package buildkite

import (
	"context"
	"fmt"
	"iter"
	"regexp"
	"time"

	buildkitelogs "github.com/buildkite/buildkite-logs"
	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

// BuildkiteLogsClient interface for dependency injection (matches upstream library interface)
type BuildkiteLogsClient interface {
	NewReader(ctx context.Context, org, pipeline, build, job string, ttl time.Duration, forceRefresh bool) (*buildkitelogs.ParquetReader, error)
}

// Verify that upstream BuildkiteLogsClient implements our interface
var _ BuildkiteLogsClient = (*buildkitelogs.Client)(nil)

// Common parameter structures for log tools
type JobLogsBaseParams struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
	JobID        string `json:"job_id"`
	CacheTTL     string `json:"cache_ttl,omitempty"`
	ForceRefresh bool   `json:"force_refresh,omitempty"`
}

type SearchLogsParams struct {
	JobLogsBaseParams
	Pattern       string `json:"pattern"`
	Context       int    `json:"context,omitempty"`
	BeforeContext int    `json:"before_context,omitempty"`
	AfterContext  int    `json:"after_context,omitempty"`
	CaseSensitive bool   `json:"case_sensitive,omitempty"`
	InvertMatch   bool   `json:"invert_match,omitempty"`
	Reverse       bool   `json:"reverse,omitempty"`
	SeekStart     int    `json:"seek_start,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}

type TailLogsParams struct {
	JobLogsBaseParams
	Tail int `json:"tail,omitempty"`
}

type ReadLogsParams struct {
	JobLogsBaseParams
	Seek  int `json:"seek,omitempty"`
	Limit int `json:"limit,omitempty"`
}

type TerseLogEntry struct {
	TS int64  `json:"ts,omitempty"`
	C  string `json:"c"`
	RN int64  `json:"rn,omitempty"`
}

// Use the library's types
type SearchResult = buildkitelogs.SearchResult

type LogResponse struct {
	Results     any   `json:"results,omitempty"`
	Entries     any   `json:"entries,omitempty"`
	MatchCount  int   `json:"match_count,omitempty"`
	TotalRows   int64 `json:"total_rows,omitempty"`
	QueryTimeMS int64 `json:"query_time_ms"`
}

// Use the library's SearchOptions
type SearchOptions = buildkitelogs.SearchOptions

// Real implementation using buildkite-logs library with injected client
func newParquetReader(ctx context.Context, client BuildkiteLogsClient, params JobLogsBaseParams) (*buildkitelogs.ParquetReader, error) {
	ttl := parseCacheTTL(params.CacheTTL)

	reader, err := client.NewReader(ctx, params.OrgSlug, params.PipelineSlug, params.BuildNumber, params.JobID, ttl, params.ForceRefresh)
	if err != nil {
		return nil, fmt.Errorf("failed to create log reader: %w", err)
	}

	return reader, nil
}

func parseCacheTTL(ttlStr string) time.Duration {
	if ttlStr == "" {
		return 30 * time.Second
	}
	duration, err := time.ParseDuration(ttlStr)
	if err != nil {
		return 30 * time.Second
	}
	return duration
}

func validateSearchPattern(pattern string) error {
	_, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid regex pattern: %w", err)
	}
	return nil
}

func formatLogEntries(entries []buildkitelogs.ParquetLogEntry) any {
	result := make([]TerseLogEntry, len(entries))
	for i, entry := range entries {
		content := entry.CleanContent(true)

		terse := TerseLogEntry{C: content, RN: entry.RowNumber}
		if entry.HasTime() {
			terse.TS = entry.Timestamp
		}

		result[i] = terse
	}
	return result
}

// SearchLogs implements the search_logs MCP tool
func SearchLogs() (mcp.Tool, mcp.ToolHandlerFor[SearchLogsParams, any], []string) {
	return mcp.Tool{
			Name:        "search_logs",
			Description: "Search log entries using regex patterns with optional context lines. For recent failures, try 'tail_logs' first, then use search_logs with patterns like 'error|failed|exception' and limit: 10-20. The json format: {ts: timestamp_ms, c: content, rn: row_number}.",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Search Logs",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, params SearchLogsParams) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.SearchLogs")
			defer span.End()

			startTime := time.Now()

			span.SetAttributes(
				attribute.String("org_slug", params.OrgSlug),
				attribute.String("pipeline_slug", params.PipelineSlug),
				attribute.String("build_number", params.BuildNumber),
				attribute.String("job_id", params.JobID),
				attribute.String("pattern", params.Pattern),
				attribute.Int("context", params.Context),
				attribute.Bool("case_sensitive", params.CaseSensitive),
				attribute.Bool("invert_match", params.InvertMatch),
				attribute.Bool("reverse", params.Reverse),
				attribute.Int("limit", params.Limit),
			)

			if err := validateSearchPattern(params.Pattern); err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			deps := DepsFromContext(ctx)
			reader, err := newParquetReader(ctx, deps.BuildkiteLogsClient, params.JobLogsBaseParams)
			if err != nil {
				return utils.NewToolResultError(fmt.Sprintf("Failed to create log reader: %v", err)), nil, nil
			}
			defer reader.Close()

			opts := SearchOptions{
				Pattern:       params.Pattern,
				CaseSensitive: params.CaseSensitive,
				InvertMatch:   params.InvertMatch,
				Reverse:       params.Reverse,
				Context:       params.Context,
				BeforeContext: params.BeforeContext,
				AfterContext:  params.AfterContext,
			}

			var results []SearchResult
			count := 0
			for result, err := range reader.SearchEntriesIter(opts) {
				if err != nil {
					return utils.NewToolResultError(fmt.Sprintf("Search error: %v", err)), nil, nil
				}

				results = append(results, result)
				count++

				// Apply limit if specified
				if params.Limit > 0 && count >= params.Limit {
					break
				}
			}

			queryTime := time.Since(startTime)
			response := LogResponse{
				Results:     results,
				MatchCount:  len(results),
				QueryTimeMS: queryTime.Milliseconds(),
			}

			span.SetAttributes(
				attribute.Int("item_count", len(results)),
			)

			return mcpTextResult(span, &response)
		},
		[]string{"read_build_logs"}
}

// TailLogs implements the tail_logs MCP tool
func TailLogs() (mcp.Tool, mcp.ToolHandlerFor[TailLogsParams, any], []string) {
	return mcp.Tool{
			Name:        "tail_logs",
			Description: "Show the last N entries from the log file. RECOMMENDED for failure diagnosis - most build failures appear in the final log entries. More token-efficient than read_logs for recent issues. The json format: {ts: timestamp_ms, c: content, rn: row_number}.",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Tail Logs",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, params TailLogsParams) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.TailLogs")
			defer span.End()

			startTime := time.Now()

			// Set defaults
			if params.Tail <= 0 {
				params.Tail = 10
			}

			span.SetAttributes(
				attribute.String("org_slug", params.OrgSlug),
				attribute.String("pipeline_slug", params.PipelineSlug),
				attribute.String("build_number", params.BuildNumber),
				attribute.String("job_id", params.JobID),
				attribute.Int("tail", params.Tail),
			)

			deps := DepsFromContext(ctx)
			reader, err := newParquetReader(ctx, deps.BuildkiteLogsClient, params.JobLogsBaseParams)
			if err != nil {
				return utils.NewToolResultError(fmt.Sprintf("Failed to create log reader: %v", err)), nil, nil
			}
			defer reader.Close()

			fileInfo, err := reader.GetFileInfo()
			if err != nil {
				return utils.NewToolResultError(fmt.Sprintf("Failed to get file info: %v", err)), nil, nil
			}

			startRow := max(fileInfo.RowCount-int64(params.Tail), 0)

			var entries []buildkitelogs.ParquetLogEntry
			for entry, err := range reader.SeekToRow(startRow) {
				if err != nil {
					return utils.NewToolResultError(fmt.Sprintf("Failed to read tail entries: %v", err)), nil, nil
				}
				entries = append(entries, entry)
			}

			queryTime := time.Since(startTime)
			formattedEntries := formatLogEntries(entries)

			response := LogResponse{
				Entries:     formattedEntries,
				TotalRows:   fileInfo.RowCount,
				QueryTimeMS: queryTime.Milliseconds(),
			}

			span.SetAttributes(
				attribute.Int("item_count", len(entries)),
			)

			return mcpTextResult(span, &response)
		},
		[]string{"read_build_logs"}
}

// ReadLogs implements the read_logs MCP tool
func ReadLogs() (mcp.Tool, mcp.ToolHandlerFor[ReadLogsParams, any], []string) {
	return mcp.Tool{
			Name:        "read_logs",
			Description: "Read log entries from the file, optionally starting from a specific row number. ALWAYS use 'limit' parameter to avoid excessive tokens. For recent failures, use 'tail_logs' instead. Recommended limits: investigation (100-500), exploration (use seek + small limits). The json format: {ts: timestamp_ms, c: content, rn: row_number}.",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Read Logs",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, params ReadLogsParams) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ReadLogs")
			defer span.End()

			startTime := time.Now()

			span.SetAttributes(
				attribute.String("org_slug", params.OrgSlug),
				attribute.String("pipeline_slug", params.PipelineSlug),
				attribute.String("build_number", params.BuildNumber),
				attribute.String("job_id", params.JobID),
				attribute.Int("seek", params.Seek),
				attribute.Int("limit", params.Limit),
			)

			deps := DepsFromContext(ctx)
			reader, err := newParquetReader(ctx, deps.BuildkiteLogsClient, params.JobLogsBaseParams)
			if err != nil {
				return utils.NewToolResultError(fmt.Sprintf("Failed to create log reader: %v", err)), nil, nil
			}
			defer reader.Close()

			var entries []buildkitelogs.ParquetLogEntry
			count := 0

			var entryIter iter.Seq2[buildkitelogs.ParquetLogEntry, error]
			if params.Seek > 0 {
				entryIter = reader.SeekToRow(int64(params.Seek))
			} else {
				entryIter = reader.ReadEntriesIter()
			}

			for entry, err := range entryIter {
				if err != nil {
					return utils.NewToolResultError(fmt.Sprintf("Failed to read entries: %v", err)), nil, nil
				}

				entries = append(entries, entry)
				count++

				// Apply limit if specified
				if params.Limit > 0 && count >= params.Limit {
					break
				}
			}

			queryTime := time.Since(startTime)
			formattedEntries := formatLogEntries(entries)

			response := LogResponse{
				Entries:     formattedEntries,
				QueryTimeMS: queryTime.Milliseconds(),
			}

			span.SetAttributes(
				attribute.Int("item_count", len(entries)),
			)

			return mcpTextResult(span, &response)
		},
		[]string{"read_build_logs"}
}
