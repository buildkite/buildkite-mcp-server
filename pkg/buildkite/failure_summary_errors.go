package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
)

const failureSummaryErrorsPerJob = 5

// JobError preserves the capture's context, including grouped findings. One
// record is not necessarily one finding; capture formats are tool-specific.
type JobError struct {
	UUID      string         `json:"uuid"`
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Context   map[string]any `json:"context"`
	Timestamp string         `json:"timestamp"`
	CreatedAt string         `json:"created_at"`
}

type JobErrorsPage struct {
	Items []JobError `json:"items"`
	Links struct {
		Self string `json:"self"`
		Next string `json:"next"`
	} `json:"links"`
}

type JobErrorsClient interface {
	ListJobErrors(ctx context.Context, org, pipeline, build, job string) (JobErrorsPage, error)
}

func (a *BuildkiteClientAdapter) ListJobErrors(ctx context.Context, org, pipeline, build, job string) (JobErrorsPage, error) {
	path := fmt.Sprintf("v2/organizations/%s/pipelines/%s/builds/%s/jobs/%s/errors?per_page=%d",
		url.PathEscape(org), url.PathEscape(pipeline), url.PathEscape(build), url.PathEscape(job), failureSummaryErrorsPerJob)
	req, err := a.NewRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return JobErrorsPage{}, err
	}
	var page JobErrorsPage
	_, err = a.Do(req, &page)
	if err == nil && page.Items == nil {
		err = fmt.Errorf("job errors response is missing items")
	}
	return page, err
}

type FailureSummaryJobErrors struct {
	Status           string     `json:"status"`
	Items            []JobError `json:"items"`
	URL              string     `json:"url,omitempty"`
	NextURL          string     `json:"next_url,omitempty"`
	Truncated        bool       `json:"truncated,omitempty"`
	ContentTruncated bool       `json:"content_truncated,omitempty"`
	Error            string     `json:"error,omitempty"`
	Hint             string     `json:"hint,omitempty"`
}

func loadFailureJobErrors(ctx context.Context, client JobErrorsClient, args GetBuildFailureSummaryArgs, jobs []FailureSummaryJob) error {
	semaphore := make(chan struct{}, failureSummaryConcurrency)
	unauthorized := make(chan error, len(jobs))
	var waitGroup sync.WaitGroup
	for i := range jobs {
		switch jobs[i].State {
		case "broken", "waiting_failed", "blocked_failed", "unblocked_failed":
			continue
		}
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			result := &FailureSummaryJobErrors{Status: "unavailable", Items: []JobError{}}
			jobs[index].JobErrors = result
			if client == nil {
				result.Error = "Job errors client is not configured"
				return
			}
			page, err := client.ListJobErrors(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, jobs[index].ID)
			if err != nil {
				if isBuildkiteUnauthorized(err) {
					unauthorized <- ErrUnauthorized
					return
				}
				result.Error, result.ContentTruncated = truncateUTF8Bytes(err.Error(), failureSummaryEntryContentByteLimit)
				result.Hint = "Structured errors could not be read; use logs. A 403 may mean read_job_errors is missing; a 404 may mean capture is not enabled."
				return
			}
			result.Status = "none_recorded"
			result.URL, result.NextURL = page.Links.Self, page.Links.Next
			result.Truncated = page.Links.Next != ""
			if len(page.Items) == 0 {
				result.Hint = "No structured errors were recorded on this page; this does not mean the job succeeded. Inspect logs; capture may be missing or still arriving."
				return
			}
			result.Status = "found"
			remaining := 64 * 1024
			for _, item := range page.Items {
				encoded, err := json.Marshal(item)
				if err != nil || len(encoded) > remaining || len(result.Items) >= failureSummaryErrorsPerJob {
					result.Truncated = true
					result.ContentTruncated = true
					break
				}
				result.Items = append(result.Items, item)
				remaining -= len(encoded)
			}
		}(i)
	}
	waitGroup.Wait()
	select {
	case err := <-unauthorized:
		return err
	default:
		return nil
	}
}

func failureSummaryWithJobErrorLimit(result *BuildFailureSummary, perJobLimit int) BuildFailureSummary {
	limited := *result
	limited.Jobs = append([]FailureSummaryJob(nil), result.Jobs...)
	for i := range limited.Jobs {
		page := result.Jobs[i].JobErrors
		if page == nil || len(page.Items) <= perJobLimit {
			continue
		}
		bounded := *page
		bounded.Items = page.Items[:perJobLimit]
		bounded.Truncated = true
		bounded.ContentTruncated = true
		limited.Jobs[i].JobErrors = &bounded
		limited.ContentTruncated = true
	}
	return limited
}
