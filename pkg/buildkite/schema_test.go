package buildkite

import (
	"reflect"
	"slices"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/require"
)

// schemaFor generates a JSON schema for the given type using the same library the MCP SDK uses.
func schemaFor[T any](t *testing.T) *jsonschema.Schema {
	t.Helper()
	schema, err := jsonschema.ForType(reflect.TypeFor[T](), &jsonschema.ForOptions{})
	require.NoError(t, err)
	require.NotNil(t, schema)
	return schema
}

func sortedRequired[T any](t *testing.T) []string {
	t.Helper()
	s := schemaFor[T](t)
	req := slices.Clone(s.Required)
	slices.Sort(req)
	return req
}

func TestListBuildsArgsSchema(t *testing.T) {
	s := schemaFor[ListBuildsArgs](t)
	req := slices.Clone(s.Required)
	slices.Sort(req)

	// Required fields: org_slug only (pipeline_slug is optional for org-wide queries)
	require.Equal(t, []string{"org_slug"}, req)

	// All fields should be present as properties
	require.Contains(t, s.Properties, "pipeline_slug")
	require.Contains(t, s.Properties, "branch")
	require.Contains(t, s.Properties, "state")
	require.Contains(t, s.Properties, "detail_level")
	require.Contains(t, s.Properties, "page")
	require.Contains(t, s.Properties, "per_page")

	// Optional fields must NOT be in required
	for _, opt := range []string{"pipeline_slug", "branch", "state", "commit", "creator", "detail_level", "page", "per_page"} {
		require.NotContains(t, s.Required, opt, "%s should be optional", opt)
	}

	// Verify descriptions are set for fields that have non-obvious info
	require.Equal(t, "Filter builds by git branch name", s.Properties["branch"].Description)
	// org_slug should have no description (field name is self-explanatory)
	require.Empty(t, s.Properties["org_slug"].Description)
}

func TestGetBuildArgsSchema(t *testing.T) {
	req := sortedRequired[GetBuildArgs](t)
	require.Equal(t, []string{"build_number", "org_slug", "pipeline_slug"}, req)
}

func TestGetPipelineArgsSchema(t *testing.T) {
	req := sortedRequired[GetPipelineArgs](t)
	require.Equal(t, []string{"org_slug", "pipeline_slug"}, req)
}

func TestCreatePipelineArgsSchema(t *testing.T) {
	req := sortedRequired[CreatePipelineArgs](t)
	// Required: org_slug, name, repository_url, cluster_id, configuration
	require.Equal(t, []string{"cluster_id", "configuration", "name", "org_slug", "repository_url"}, req)
}

func TestUpdatePipelineArgsSchema(t *testing.T) {
	req := sortedRequired[UpdatePipelineArgs](t)
	// Required: org_slug, pipeline_slug, repository_url, configuration
	require.Equal(t, []string{"configuration", "org_slug", "pipeline_slug", "repository_url"}, req)
}

func TestCreateBuildArgsSchema(t *testing.T) {
	req := sortedRequired[CreateBuildArgs](t)
	require.Equal(t, []string{"branch", "commit", "message", "org_slug", "pipeline_slug"}, req)
}

func TestListAnnotationsArgsSchema(t *testing.T) {
	s := schemaFor[ListAnnotationsArgs](t)
	req := slices.Clone(s.Required)
	slices.Sort(req)
	require.Equal(t, []string{"build_number", "org_slug", "pipeline_slug"}, req)

	for _, opt := range []string{"page", "per_page"} {
		require.NotContains(t, s.Required, opt, "%s should be optional", opt)
	}
}

func TestGetFailedTestExecutionsArgsSchema(t *testing.T) {
	s := schemaFor[GetFailedTestExecutionsArgs](t)
	req := slices.Clone(s.Required)
	slices.Sort(req)
	require.Equal(t, []string{"org_slug", "run_id", "test_suite_slug"}, req)

	for _, opt := range []string{"include_failure_expanded", "page", "per_page"} {
		require.NotContains(t, s.Required, opt, "%s should be optional", opt)
	}
}

func TestReadLogsParamsSchema(t *testing.T) {
	s := schemaFor[ReadLogsParams](t)
	req := slices.Clone(s.Required)
	slices.Sort(req)
	require.Equal(t, []string{"build_number", "job_id", "org_slug", "pipeline_slug"}, req)

	for _, opt := range []string{"cache_ttl", "force_refresh", "seek", "limit"} {
		require.NotContains(t, s.Required, opt, "%s should be optional", opt)
	}
}

func TestSearchLogsParamsSchema(t *testing.T) {
	s := schemaFor[SearchLogsParams](t)
	req := slices.Clone(s.Required)
	slices.Sort(req)
	require.Equal(t, []string{"build_number", "job_id", "org_slug", "pattern", "pipeline_slug"}, req)

	for _, opt := range []string{"cache_ttl", "force_refresh", "context", "before_context", "after_context", "case_sensitive", "invert_match", "reverse", "seek_start", "limit"} {
		require.NotContains(t, s.Required, opt, "%s should be optional", opt)
	}
}

func TestTailLogsParamsSchema(t *testing.T) {
	s := schemaFor[TailLogsParams](t)
	req := slices.Clone(s.Required)
	slices.Sort(req)
	require.Equal(t, []string{"build_number", "job_id", "org_slug", "pipeline_slug"}, req)

	for _, opt := range []string{"cache_ttl", "force_refresh", "tail"} {
		require.NotContains(t, s.Required, opt, "%s should be optional", opt)
	}
}

func TestListArtifactsForBuildArgsSchema(t *testing.T) {
	s := schemaFor[ListArtifactsForBuildArgs](t)
	req := slices.Clone(s.Required)
	slices.Sort(req)
	require.Equal(t, []string{"build_number", "org_slug", "pipeline_slug"}, req)

	for _, opt := range []string{"page", "per_page"} {
		require.NotContains(t, s.Required, opt, "%s should be optional", opt)
	}
}

func TestListArtifactsForJobArgsSchema(t *testing.T) {
	s := schemaFor[ListArtifactsForJobArgs](t)
	req := slices.Clone(s.Required)
	slices.Sort(req)
	require.Equal(t, []string{"build_number", "job_id", "org_slug", "pipeline_slug"}, req)

	for _, opt := range []string{"page", "per_page"} {
		require.NotContains(t, s.Required, opt, "%s should be optional", opt)
	}
}

func TestGetArtifactArgsSchema(t *testing.T) {
	req := sortedRequired[GetArtifactArgs](t)
	require.Equal(t, []string{"url"}, req)
}

func TestGetTestArgsSchema(t *testing.T) {
	req := sortedRequired[GetTestArgs](t)
	require.Equal(t, []string{"org_slug", "test_id", "test_suite_slug"}, req)
}

func TestListTestRunsArgsSchema(t *testing.T) {
	s := schemaFor[ListTestRunsArgs](t)
	req := slices.Clone(s.Required)
	slices.Sort(req)
	require.Equal(t, []string{"org_slug", "test_suite_slug"}, req)

	for _, opt := range []string{"page", "per_page"} {
		require.NotContains(t, s.Required, opt, "%s should be optional", opt)
	}
}

func TestGetTestRunArgsSchema(t *testing.T) {
	req := sortedRequired[GetTestRunArgs](t)
	require.Equal(t, []string{"org_slug", "run_id", "test_suite_slug"}, req)
}

func TestListPipelinesArgsSchema(t *testing.T) {
	s := schemaFor[ListPipelinesArgs](t)
	req := slices.Clone(s.Required)
	slices.Sort(req)
	require.Equal(t, []string{"org_slug"}, req)

	for _, opt := range []string{"name", "repository", "page", "per_page", "detail_level"} {
		require.NotContains(t, s.Required, opt, "%s should be optional", opt)
	}
}

func TestUnblockJobArgsSchema(t *testing.T) {
	s := schemaFor[UnblockJobArgs](t)
	req := slices.Clone(s.Required)
	slices.Sort(req)
	require.Equal(t, []string{"build_number", "job_id", "org_slug", "pipeline_slug"}, req)

	for _, opt := range []string{"fields"} {
		require.NotContains(t, s.Required, opt, "%s should be optional", opt)
	}
}

func TestListClustersArgsSchema(t *testing.T) {
	s := schemaFor[ListClustersArgs](t)
	req := slices.Clone(s.Required)
	slices.Sort(req)
	require.Equal(t, []string{"org_slug"}, req)

	for _, opt := range []string{"page", "per_page"} {
		require.NotContains(t, s.Required, opt, "%s should be optional", opt)
	}
}

func TestGetClusterArgsSchema(t *testing.T) {
	req := sortedRequired[GetClusterArgs](t)
	require.Equal(t, []string{"cluster_id", "org_slug"}, req)
}

func TestListClusterQueuesArgsSchema(t *testing.T) {
	s := schemaFor[ListClusterQueuesArgs](t)
	req := slices.Clone(s.Required)
	slices.Sort(req)
	require.Equal(t, []string{"cluster_id", "org_slug"}, req)

	for _, opt := range []string{"page", "per_page"} {
		require.NotContains(t, s.Required, opt, "%s should be optional", opt)
	}
}

func TestGetClusterQueueArgsSchema(t *testing.T) {
	req := sortedRequired[GetClusterQueueArgs](t)
	require.Equal(t, []string{"cluster_id", "org_slug", "queue_id"}, req)
}

func TestGetBuildTestEngineRunsArgsSchema(t *testing.T) {
	req := sortedRequired[GetBuildTestEngineRunsArgs](t)
	require.Equal(t, []string{"build_number", "org_slug", "pipeline_slug"}, req)
}
