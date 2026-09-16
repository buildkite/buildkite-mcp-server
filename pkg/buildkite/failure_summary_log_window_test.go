package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	buildkitelogs "github.com/buildkite/buildkite-logs"
	"github.com/buildkite/buildkite-logs/logparser"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/stretchr/testify/require"
)

// writeTestParquetLogWithGroups writes a synthetic job log where each row
// carries the group of the most recent "~~~ ", "--- " or "+++ " header, the
// way the agent log parser assigns groups.
func writeTestParquetLogWithGroups(t *testing.T, filename string, contents []string) {
	t.Helper()

	f, err := os.Create(filename)
	require.NoError(t, err)
	defer f.Close()

	writer, err := buildkitelogs.NewParquetWriter(f)
	require.NoError(t, err)
	defer writer.Close()

	baseTime := time.Date(2026, 9, 16, 0, 11, 43, 0, time.UTC)
	group := ""
	entries := make([]*logparser.Entry, len(contents))
	for i, content := range contents {
		if strings.HasPrefix(content, "~~~ ") || strings.HasPrefix(content, "--- ") || strings.HasPrefix(content, "+++ ") {
			group = content
		}
		entries[i] = &logparser.Entry{
			Timestamp: baseTime.Add(time.Duration(i) * 100 * time.Millisecond),
			Content:   content,
			RawLine:   []byte(content),
			Group:     group,
		}
	}

	require.NoError(t, writer.WriteBatch(entries))
}

// failureSummaryForLog runs get_build_failure_summary for a build with one
// failed job whose log is the given parquet file and returns that job from
// the decoded tool output, so log-window behaviour is asserted on what a
// client actually receives.
func failureSummaryForLog(t *testing.T, logPath string, logTail int) FailureSummaryJob {
	t.Helper()

	_, handler, _ := GetBuildFailureSummary()
	ctx := ContextWithDeps(context.Background(), ToolDependencies{
		BuildsClient: &MockBuildsClient{
			GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
				return buildkite.Build{ID: "build-id", Number: 1, State: "failed"}, &buildkite.Response{}, nil
			},
		},
		JobsClient: &MockJobsClient{
			ListByBuildFunc: func(_ context.Context, _, _, _ string, options *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
				if options.State[0] == "failed" {
					return buildkite.JobsList{Items: []buildkite.Job{{ID: "job-id", State: "failed"}}}, &buildkite.Response{}, nil
				}
				return buildkite.JobsList{}, &buildkite.Response{}, nil
			},
		},
		BuildkiteLogsClient: &MockBuildkiteLogsClient{
			NewReaderFunc: func(context.Context, string, string, string, string, time.Duration, bool) (*buildkitelogs.ParquetReader, error) {
				return buildkitelogs.NewParquetReader(logPath), nil
			},
		},
	})

	include := false
	callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
		OrgSlug:            "org",
		PipelineSlug:       "pipeline",
		BuildNumber:        "1",
		LogTail:            logTail,
		IncludeAnnotations: &include,
		IncludeFailedTests: &include,
	})
	require.NoError(t, err)
	require.False(t, callResult.IsError)

	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(getTextResult(t, callResult).Text), &summary))
	require.Len(t, summary.Jobs, 1)
	return summary.Jobs[0]
}

func logTailContents(job FailureSummaryJob) []string {
	contents := make([]string, len(job.LogTail))
	for i, entry := range job.LogTail {
		contents[i] = entry.C
	}
	return contents
}

// agentFailureLog mirrors the shape of a real docker-compose job log: command
// output, the agent's failure marker (colored, as the agent prints it when
// ANSI is enabled), plugin post-command hooks, then cleanup output.
func agentFailureLog(commandLines, cleanupLines int) []string {
	lines := []string{"~~~ Preparing working directory", "$ git checkout", "+++ :rspec: Running specs"}
	for i := range commandLines {
		lines = append(lines, fmt.Sprintf("command output %02d", i))
	}
	lines = append(lines,
		"^^^ +++",
		"\x1b[31m🚨 Error: The command exited with status 1\x1b[0m",
		"^^^ +++",
		"~~~",
		"$ .buildkite/plugins/coverage/hooks/post-command",
		"Skipping coverage upload because command exited 1",
		"user command error: running \"plugin docker-compose-buildkite-plugin command\" shell hook: the plugin docker-compose-buildkite-plugin command hook exited with status 1",
		"~~~ :docker: Cleaning up after docker-compose",
	)
	for i := range cleanupLines {
		lines = append(lines, fmt.Sprintf("Container app-%02d Removed", i))
	}
	return append(lines, "~~~ Running agent pre-exit hook", "$ /buildkite/agent/hooks/pre-exit")
}

func TestGetBuildFailureSummaryLogWindow(t *testing.T) {
	t.Run("ends just after the agent failure marker instead of the log tail", func(t *testing.T) {
		lines := agentFailureLog(30, 40)
		logPath := t.TempDir() + "/failed.parquet"
		writeTestParquetLogWithGroups(t, logPath, lines)

		job := failureSummaryForLog(t, logPath, 10)

		// Rows: 0-2 headers, 3-32 command output, 33 "^^^ +++", 34 marker,
		// 35 "^^^ +++", 36 bare "~~~" opens the post-command hook.
		require.Equal(t, "error_anchor", job.LogSelection)
		require.NotNil(t, job.LogAnchorRN)
		require.Equal(t, int64(34), *job.LogAnchorRN)
		require.Equal(t, "+++ :rspec: Running specs", job.LogAnchorGroup)
		require.Equal(t, []string{
			"command output 23", "command output 24", "command output 25", "command output 26",
			"command output 27", "command output 28", "command output 29",
			"^^^ +++",
			"🚨 Error: The command exited with status 1",
			"^^^ +++",
		}, logTailContents(job))
		require.Equal(t, int64(26), job.LogTail[0].RN)
		require.Equal(t, int64(35), job.LogTail[len(job.LogTail)-1].RN)
		require.Equal(t, int64(len(lines)), job.LogTotalRows)
		require.True(t, job.LogTruncated)
		require.Zero(t, job.LogEntriesOmitted)
	})

	t.Run("keeps hook error rows after the marker until the next group opens", func(t *testing.T) {
		lines := []string{
			"+++ :graphql: Running GraphQL Schema Comparison",
			"schema changed",
			"^^^ +++",
			"🚨 Error: The command exited with status 1",
			"^^^ +++",
			"user command error: the plugin docker-compose-buildkite-plugin command hook exited with status 1",
			"~~~",
			"$ /buildkite/plugins/docker-compose/hooks/pre-exit",
			"~~~ :docker: Cleaning up after docker-compose",
			"no container to kill",
		}
		logPath := t.TempDir() + "/failed.parquet"
		writeTestParquetLogWithGroups(t, logPath, lines)

		job := failureSummaryForLog(t, logPath, 50)

		require.Equal(t, "error_anchor", job.LogSelection)
		require.Equal(t, lines[:6], logTailContents(job))
		require.False(t, job.LogTruncated)
	})

	t.Run("caps rows kept after the marker when no group boundary follows", func(t *testing.T) {
		lines := []string{"+++ build", "compiling", "🚨 Error: The command exited with status 2"}
		for i := range 20 {
			lines = append(lines, fmt.Sprintf("trailing %02d", i))
		}
		logPath := t.TempDir() + "/failed.parquet"
		writeTestParquetLogWithGroups(t, logPath, lines)

		job := failureSummaryForLog(t, logPath, 50)

		require.Equal(t, "error_anchor", job.LogSelection)
		require.Equal(t, lines[:3+failureSummaryLogAnchorTrailingRows], logTailContents(job))
	})

	t.Run("anchors on the first marker when hooks fail after the command", func(t *testing.T) {
		lines := []string{
			"+++ :test: Running tests",
			"1 example, 1 failure",
			"🚨 Error: The command exited with status 1",
			"~~~ Running plugin upload post-command hook",
			"upload failed: no results file",
			"🚨 Error: running \"plugin upload post-command\" shell hook: the plugin upload post-command hook exited with status 1",
			"~~~ Running agent pre-exit hook",
			"$ /buildkite/agent/hooks/pre-exit",
		}
		logPath := t.TempDir() + "/failed.parquet"
		writeTestParquetLogWithGroups(t, logPath, lines)

		job := failureSummaryForLog(t, logPath, 50)

		require.Equal(t, "error_anchor", job.LogSelection)
		require.Equal(t, int64(2), *job.LogAnchorRN)
		require.Equal(t, "+++ :test: Running tests", job.LogAnchorGroup)
		require.Equal(t, lines[:3], logTailContents(job))
	})

	t.Run("falls back to the plain tail when no marker is present", func(t *testing.T) {
		lines := []string{"~~~ Running tests", "still running"}
		for i := range 30 {
			lines = append(lines, fmt.Sprintf("cleanup %02d", i))
		}
		logPath := t.TempDir() + "/timed-out.parquet"
		writeTestParquetLogWithGroups(t, logPath, lines)

		job := failureSummaryForLog(t, logPath, 5)

		require.Equal(t, "tail", job.LogSelection)
		require.Nil(t, job.LogAnchorRN)
		require.Empty(t, job.LogAnchorGroup)
		require.Equal(t, lines[len(lines)-5:], logTailContents(job))
		require.Equal(t, int64(len(lines)-1), job.LogTail[len(job.LogTail)-1].RN)
		require.True(t, job.LogTruncated)
	})

	t.Run("ignores a marker older than the scan window", func(t *testing.T) {
		lines := []string{"~~~ Setup", "🚨 Error: Error flushing redactors: closed", "continuing anyway"}
		for i := range failureSummaryLogAnchorScanRows + 50 {
			lines = append(lines, fmt.Sprintf("output %04d", i))
		}
		logPath := t.TempDir() + "/long.parquet"
		writeTestParquetLogWithGroups(t, logPath, lines)

		job := failureSummaryForLog(t, logPath, 3)

		require.Equal(t, "tail", job.LogSelection)
		require.Nil(t, job.LogAnchorRN)
		require.Equal(t, lines[len(lines)-3:], logTailContents(job))
	})

	t.Run("a bare ^^^ +++ expansion marker is not treated as a failure", func(t *testing.T) {
		lines := []string{
			"~~~ Detected protected environment variables",
			"Ignored BUILDKITE_BUILD_PATH",
			"^^^ +++",
			"+++ :hammer: Building",
			"done",
			"~~~ Running agent pre-exit hook",
		}
		logPath := t.TempDir() + "/canceled.parquet"
		writeTestParquetLogWithGroups(t, logPath, lines)

		job := failureSummaryForLog(t, logPath, 50)

		require.Equal(t, "tail", job.LogSelection)
		require.Equal(t, lines, logTailContents(job))
	})
}
