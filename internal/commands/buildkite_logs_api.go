package commands

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	buildkitelogs "github.com/buildkite/buildkite-logs"
	gobuildkite "github.com/buildkite/go-buildkite/v5"
)

type buildkiteLogsAPI struct {
	client *gobuildkite.Client
}

func NewBuildkiteLogsAPI(client *gobuildkite.Client) *buildkiteLogsAPI {
	return &buildkiteLogsAPI{client: client}
}

func (a *buildkiteLogsAPI) GetJobLog(ctx context.Context, org, pipeline, build, job string) (io.ReadCloser, error) {
	jobLog, _, err := a.client.Jobs.GetJobLog(ctx, org, pipeline, build, job)
	if err != nil {
		return nil, fmt.Errorf("failed to get job log: %w", err)
	}

	return io.NopCloser(strings.NewReader(jobLog.Content)), nil
}

func (a *buildkiteLogsAPI) GetJobStatus(ctx context.Context, org, pipeline, build, jobID string) (*buildkitelogs.JobStatus, error) {
	buildInfo, _, err := a.client.Builds.Get(ctx, org, pipeline, build, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get build info: %w", err)
	}

	for _, job := range buildInfo.Jobs {
		if job.ID == jobID {
			state := buildkitelogs.JobState(job.State)
			return &buildkitelogs.JobStatus{
				ID:         job.ID,
				State:      state,
				IsTerminal: buildkitelogs.IsTerminalState(state),
				WebURL:     job.WebURL,
				ExitStatus: job.ExitStatus,
				FinishedAt: timestampTime(job.FinishedAt),
			}, nil
		}
	}

	for _, job := range buildInfo.Jobs {
		if job.RetrySource != nil && job.RetrySource.JobID == jobID {
			return &buildkitelogs.JobStatus{
				ID:         jobID,
				State:      buildkitelogs.JobStateFailed,
				IsTerminal: true,
			}, nil
		}
	}

	return nil, fmt.Errorf("job not found: %s", jobID)
}

func timestampTime(timestamp *gobuildkite.Timestamp) *time.Time {
	if timestamp == nil {
		return nil
	}
	return &timestamp.Time
}
