package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/stretchr/testify/require"
)

func failureSummaryArtifactsTestDeps(artifactsClient ArtifactsClient) ToolDependencies {
	return ToolDependencies{
		BuildsClient: &MockBuildsClient{
			GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
				return buildkite.Build{Number: 1, State: "failed"}, &buildkite.Response{}, nil
			},
		},
		JobsClient: &MockJobsClient{
			ListByBuildFunc: func(context.Context, string, string, string, *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
				return buildkite.JobsList{}, &buildkite.Response{}, nil
			},
		},
		ArtifactsClient: artifactsClient,
	}
}

func failureSummaryTestArtifacts(count int) []buildkite.Artifact {
	artifacts := make([]buildkite.Artifact, count)
	for i := range artifacts {
		artifacts[i] = buildkite.Artifact{
			ID:          fmt.Sprintf("artifact-%d", i),
			JobID:       "job-failed",
			State:       "finished",
			Path:        fmt.Sprintf("log/test/spec_%d.log", i),
			Filename:    fmt.Sprintf("spec_%d.log", i),
			MimeType:    "text/plain",
			FileSize:    1024,
			SHA1:        "abc123",
			DownloadURL: "https://api.buildkite.com/v2/download",
		}
	}
	return artifacts
}

func callFailureSummaryForArtifacts(t *testing.T, deps ToolDependencies, args GetBuildFailureSummaryArgs) BuildFailureSummary {
	t.Helper()
	args.OrgSlug, args.PipelineSlug, args.BuildNumber = "org", "pipeline", "1"
	_, handler, _ := GetBuildFailureSummary()
	callResult, _, err := handler(ContextWithDeps(context.Background(), deps), createMCPRequest(t, map[string]any{}), args)
	require.NoError(t, err)
	require.False(t, callResult.IsError)
	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(getTextResult(t, callResult).Text), &summary))
	return summary
}

func TestGetBuildFailureSummaryListsFirstPageOfArtifacts(t *testing.T) {
	var calls int
	artifactsClient := &MockArtifactsClient{
		ListByBuildFunc: func(_ context.Context, org, pipeline, number string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error) {
			calls++
			require.Equal(t, "org", org)
			require.Equal(t, "pipeline", pipeline)
			require.Equal(t, "1", number)
			require.Equal(t, 1, opts.Page)
			require.Equal(t, defaultFailureSummaryArtifacts, opts.PerPage)
			return failureSummaryTestArtifacts(defaultFailureSummaryArtifacts), &buildkite.Response{NextPage: 2}, nil
		},
	}

	summary := callFailureSummaryForArtifacts(t, failureSummaryArtifactsTestDeps(artifactsClient), GetBuildFailureSummaryArgs{})

	require.Equal(t, 1, calls)
	require.Len(t, summary.Artifacts, defaultFailureSummaryArtifacts)
	require.True(t, summary.ArtifactsTruncated)
	require.Equal(t, artifactListItem{
		ID:       "artifact-0",
		JobID:    "job-failed",
		State:    "finished",
		Path:     "log/test/spec_0.log",
		Filename: "spec_0.log",
		MimeType: "text/plain",
		FileSize: 1024,
		SHA1:     "abc123",
	}, summary.Artifacts[0])
	require.Empty(t, summary.Warnings)
}

func TestGetBuildFailureSummaryArtifactsNotTruncatedOnLastPage(t *testing.T) {
	artifactsClient := &MockArtifactsClient{
		ListByBuildFunc: func(context.Context, string, string, string, *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error) {
			return failureSummaryTestArtifacts(2), &buildkite.Response{}, nil
		},
	}

	summary := callFailureSummaryForArtifacts(t, failureSummaryArtifactsTestDeps(artifactsClient), GetBuildFailureSummaryArgs{})

	require.Len(t, summary.Artifacts, 2)
	require.False(t, summary.ArtifactsTruncated)
}

func TestGetBuildFailureSummaryBoundsMaxArtifacts(t *testing.T) {
	for _, tc := range []struct {
		name        string
		requested   int
		wantPerPage int
	}{
		{name: "custom", requested: 3, wantPerPage: 3},
		{name: "capped", requested: 500, wantPerPage: maxFailureSummaryArtifacts},
		{name: "default for negative", requested: -1, wantPerPage: defaultFailureSummaryArtifacts},
	} {
		t.Run(tc.name, func(t *testing.T) {
			artifactsClient := &MockArtifactsClient{
				ListByBuildFunc: func(_ context.Context, _, _, _ string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error) {
					require.Equal(t, 1, opts.Page)
					require.Equal(t, tc.wantPerPage, opts.PerPage)
					return nil, &buildkite.Response{}, nil
				},
			}

			summary := callFailureSummaryForArtifacts(t, failureSummaryArtifactsTestDeps(artifactsClient), GetBuildFailureSummaryArgs{MaxArtifacts: tc.requested})
			require.Empty(t, summary.Artifacts)
			require.False(t, summary.ArtifactsTruncated)
		})
	}
}

func TestGetBuildFailureSummaryCanDisableArtifacts(t *testing.T) {
	artifactsClient := &MockArtifactsClient{
		ListByBuildFunc: func(context.Context, string, string, string, *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error) {
			t.Fatal("artifacts must not be listed when include_artifacts is false")
			return nil, nil, nil
		},
	}

	summary := callFailureSummaryForArtifacts(t, failureSummaryArtifactsTestDeps(artifactsClient), GetBuildFailureSummaryArgs{IncludeArtifacts: boolPtr(false)})

	require.Empty(t, summary.Artifacts)
}

func TestGetBuildFailureSummaryArtifactErrorBecomesWarning(t *testing.T) {
	forbidden := &buildkite.ErrorResponse{
		Response: &http.Response{
			StatusCode: http.StatusForbidden,
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    &url.URL{Scheme: "https", Host: "api.buildkite.com"},
			},
		},
		Message: "Your access token doesn't have the required scope",
	}
	artifactsClient := &MockArtifactsClient{
		ListByBuildFunc: func(context.Context, string, string, string, *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error) {
			return nil, nil, forbidden
		},
	}

	summary := callFailureSummaryForArtifacts(t, failureSummaryArtifactsTestDeps(artifactsClient), GetBuildFailureSummaryArgs{})

	require.Equal(t, "failed", summary.Build.State)
	require.Empty(t, summary.Artifacts)
	require.Len(t, summary.Warnings, 1)
	require.Contains(t, summary.Warnings[0], "artifacts unavailable")
	require.Contains(t, summary.Warnings[0], forbidden.Message)
}

func TestLoadFailureArtifactsPropagatesUnauthorized(t *testing.T) {
	unauthorized := fmt.Errorf("wrapped API failure: %w", &buildkite.ErrorResponse{
		Response: &http.Response{
			StatusCode: http.StatusUnauthorized,
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    &url.URL{Scheme: "https", Host: "api.buildkite.com"},
			},
		},
	})
	client := &MockArtifactsClient{
		ListByBuildFunc: func(context.Context, string, string, string, *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error) {
			return nil, nil, unauthorized
		},
	}

	_, _, err := loadFailureArtifacts(context.Background(), client, GetBuildFailureSummaryArgs{OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1"}, 1)
	require.ErrorIs(t, err, ErrUnauthorized)
}

func TestLoadFailureArtifactsTrimsOversizedPage(t *testing.T) {
	client := &MockArtifactsClient{
		ListByBuildFunc: func(context.Context, string, string, string, *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error) {
			return failureSummaryTestArtifacts(5), &buildkite.Response{}, nil
		},
	}

	artifacts, truncated, err := loadFailureArtifacts(context.Background(), client, GetBuildFailureSummaryArgs{}, 3)
	require.NoError(t, err)
	require.Len(t, artifacts, 3)
	require.True(t, truncated)
}

func TestLimitFailureSummaryCollectionsDropsArtifactsBeforeAnnotations(t *testing.T) {
	annotations := make([]FailureSummaryAnnotation, 3)
	for i := range annotations {
		annotations[i] = FailureSummaryAnnotation{
			AnnotationSummary: AnnotationSummary{ID: fmt.Sprintf("annotation-%d", i), Style: "error"},
			BodyHTML:          "failure",
		}
	}
	result := BuildFailureSummary{
		Build:       BuildFailureSummaryBuild{BuildSummary: BuildSummary{Number: 1, State: "failed"}},
		Annotations: annotations,
		Artifacts:   toArtifactListItems(failureSummaryTestArtifacts(maxFailureSummaryArtifacts)),
	}

	// A limit that fits every annotation plus a few artifacts at their
	// strings-emptied floor forces artifacts, and only artifacts, to shrink.
	fewArtifacts := failureSummaryWithArtifactLimit(&result, 5)
	limit, err := failureSummaryStructureBytes(&fewArtifacts, failureSummaryContentByteLimit)
	require.NoError(t, err)

	require.NoError(t, limitFailureSummaryCollections(&result, limit))

	require.Len(t, result.Annotations, len(annotations))
	require.False(t, result.AnnotationsTruncated)
	require.GreaterOrEqual(t, len(result.Artifacts), 5)
	require.Less(t, len(result.Artifacts), maxFailureSummaryArtifacts)
	require.True(t, result.ArtifactsTruncated)
	require.True(t, result.ContentTruncated)
}
