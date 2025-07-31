package buildkite

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/stretchr/testify/require"
)

// MockJobsClient implements JobsClient interface for testing
type MockJobsClient struct {
	UnblockJobFunc func(ctx context.Context, org, pipeline, buildNumber, jobID string, opt *buildkite.JobUnblockOptions) (buildkite.Job, *buildkite.Response, error)
}

func (m *MockJobsClient) UnblockJob(ctx context.Context, org, pipeline, buildNumber, jobID string, opt *buildkite.JobUnblockOptions) (buildkite.Job, *buildkite.Response, error) {
	if m.UnblockJobFunc != nil {
		return m.UnblockJobFunc(ctx, org, pipeline, buildNumber, jobID, opt)
	}
	return buildkite.Job{}, nil, nil
}

func TestUnblockJob(t *testing.T) {
	tests := []struct {
		name          string
		args          map[string]interface{}
		mockResponse  buildkite.Job
		mockError     error
		expectedError string
	}{
		{
			name: "successful unblock",
			args: map[string]interface{}{
				"org_slug":      "my-org",
				"pipeline_slug": "my-pipeline",
				"build_number":  "123",
				"job_id":        "job-uuid-123",
			},
			mockResponse: buildkite.Job{
				ID:    "job-uuid-123",
				State: "unblocked",
				Type:  "manual",
			},
		},
		{
			name: "successful unblock with fields",
			args: map[string]interface{}{
				"org_slug":      "my-org",
				"pipeline_slug": "my-pipeline",
				"build_number":  "123",
				"job_id":        "job-uuid-123",
				"fields": map[string]string{
					"release-name": "v2.0.0",
					"environment":  "production",
				},
			},
			mockResponse: buildkite.Job{
				ID:    "job-uuid-123",
				State: "unblocked",
				Type:  "manual",
			},
		},
		{
			name: "missing org_slug",
			args: map[string]interface{}{
				"pipeline_slug": "my-pipeline",
				"build_number":  "123",
				"job_id":        "job-uuid-123",
			},
			expectedError: "org_slug is required",
		},
		{
			name: "missing pipeline_slug",
			args: map[string]interface{}{
				"org_slug":     "my-org",
				"build_number": "123",
				"job_id":       "job-uuid-123",
			},
			expectedError: "pipeline_slug is required",
		},
		{
			name: "missing build_number",
			args: map[string]interface{}{
				"org_slug":      "my-org",
				"pipeline_slug": "my-pipeline",
				"job_id":        "job-uuid-123",
			},
			expectedError: "build_number is required",
		},
		{
			name: "missing job_id",
			args: map[string]interface{}{
				"org_slug":      "my-org",
				"pipeline_slug": "my-pipeline",
				"build_number":  "123",
			},
			expectedError: "job_id is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockJobsClient{
				UnblockJobFunc: func(ctx context.Context, org, pipeline, buildNumber, jobID string, opt *buildkite.JobUnblockOptions) (buildkite.Job, *buildkite.Response, error) {
					if tt.expectedError == "" {
						// Verify the parameters passed to the client
						require.Equal(t, tt.args["org_slug"], org)
						require.Equal(t, tt.args["pipeline_slug"], pipeline)
						require.Equal(t, tt.args["build_number"], buildNumber)
						require.Equal(t, tt.args["job_id"], jobID)

						// Verify fields are passed correctly if provided
						if tt.args["fields"] != nil {
							require.NotNil(t, opt)
							expectedFields, ok := tt.args["fields"].(map[string]string)
							require.True(t, ok)
							require.Equal(t, expectedFields, opt.Fields)
						}
					}

					if tt.mockError != nil {
						return buildkite.Job{}, nil, tt.mockError
					}

					return tt.mockResponse, &buildkite.Response{
						Response: &http.Response{StatusCode: http.StatusOK},
					}, nil
				},
			}

			tool, handler := UnblockJob(mockClient)

			// Create a mock request using the helper function
			request := createMCPRequest(t, tt.args)

			// Convert to UnblockJobArgs for the typed handler
			args := UnblockJobArgs{
				OrgSlug:      getStringValue(tt.args, "org_slug"),
				PipelineSlug: getStringValue(tt.args, "pipeline_slug"),
				BuildNumber:  getStringValue(tt.args, "build_number"),
				JobID:        getStringValue(tt.args, "job_id"),
			}
			if fieldsMap, ok := tt.args["fields"].(map[string]string); ok {
				args.Fields = fieldsMap
			}

			result, err := handler(context.Background(), request, args)

			if tt.expectedError != "" {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.True(t, result.IsError)
				textContent := getTextResult(t, result)
				require.Contains(t, textContent.Text, tt.expectedError)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.False(t, result.IsError)

				// Verify the response contains the expected job data
				if tt.mockResponse.ID != "" {
					textContent := getTextResult(t, result)
					var job buildkite.Job
					err := json.Unmarshal([]byte(textContent.Text), &job)
					require.NoError(t, err)
					require.Equal(t, tt.mockResponse.ID, job.ID)
					require.Equal(t, tt.mockResponse.State, job.State)
				}
			}

			// Verify tool metadata
			require.Equal(t, "unblock_job", tool.Name)
			require.Contains(t, tool.Description, "Unblock a blocked job")
			require.False(t, *tool.Annotations.ReadOnlyHint)
		})
	}
}

// Helper function to get string value from interface map
func getStringValue(args map[string]interface{}, key string) string {
	if val, ok := args[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}