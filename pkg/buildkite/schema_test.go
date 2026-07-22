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
	require.Contains(t, s.Properties, "page")
	require.Contains(t, s.Properties, "per_page")

	// Optional fields must NOT be in required
	for _, opt := range []string{"pipeline_slug", "branch", "state", "commit", "creator", "page", "per_page"} {
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
	require.Equal(t, []string{"org_slug", "pipeline_slug"}, req)
}

func TestListPipelineSchedulesArgsSchema(t *testing.T) {
	s := schemaFor[ListPipelineSchedulesArgs](t)
	req := slices.Clone(s.Required)
	slices.Sort(req)
	require.Equal(t, []string{"org_slug", "pipeline_slug"}, req)

	for _, opt := range []string{"page", "per_page"} {
		require.NotContains(t, s.Required, opt, "%s should be optional", opt)
	}
}

func TestGetPipelineScheduleArgsSchema(t *testing.T) {
	req := sortedRequired[GetPipelineScheduleArgs](t)
	require.Equal(t, []string{"org_slug", "pipeline_slug", "schedule_id"}, req)
}

func TestCreatePipelineScheduleArgsSchema(t *testing.T) {
	s := schemaFor[CreatePipelineScheduleArgs](t)
	req := slices.Clone(s.Required)
	slices.Sort(req)
	require.Equal(t, []string{"cronline", "org_slug", "pipeline_slug"}, req)

	for _, opt := range []string{"label", "message", "commit", "branch", "env", "enabled"} {
		require.NotContains(t, s.Required, opt, "%s should be optional", opt)
	}
}

func TestUpdatePipelineScheduleArgsSchema(t *testing.T) {
	s := schemaFor[UpdatePipelineScheduleArgs](t)
	req := slices.Clone(s.Required)
	slices.Sort(req)
	require.Equal(t, []string{"org_slug", "pipeline_slug", "schedule_id"}, req)

	for _, opt := range []string{"cronline", "label", "message", "commit", "branch", "env", "enabled"} {
		require.NotContains(t, s.Required, opt, "%s should be optional", opt)
	}
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
	require.Equal(t, []string{"artifact_id", "build_number", "job_id", "org_slug", "pipeline_slug"}, req)
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

func TestListJobsArgsSchemaFilters(t *testing.T) {
	s := schemaFor[ListJobsArgs](t)
	require.Equal(t, []string{"build_number", "org_slug", "pipeline_slug"}, sortedRequired[ListJobsArgs](t))

	for _, opt := range []string{"step_key", "group_key"} {
		require.Contains(t, s.Properties, opt)
		require.NotContains(t, s.Required, opt, "%s should be optional", opt)
	}

	require.Equal(t, "Filter jobs by step key. Includes all parallel jobs for the step", s.Properties["step_key"].Description)
	require.Equal(t, "Filter jobs by group key. Includes all jobs in the group", s.Properties["group_key"].Description)
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

func TestListJobsArgsSchemaOptionalFields(t *testing.T) {
	s := schemaFor[ListJobsArgs](t)
	req := slices.Clone(s.Required)
	slices.Sort(req)
	require.Equal(t, []string{"build_number", "org_slug", "pipeline_slug"}, req)

	for _, opt := range []string{"state", "detail_level", "include_retried_jobs", "per_page", "after", "before", "include_agent"} {
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

func TestListAgentsArgsSchema(t *testing.T) {
	s := schemaFor[ListAgentsArgs](t)
	req := slices.Clone(s.Required)
	slices.Sort(req)
	require.Equal(t, []string{"org_slug"}, req)

	for _, opt := range []string{"name", "hostname", "version", "page", "per_page", "detail_level"} {
		require.NotContains(t, s.Required, opt, "%s should be optional", opt)
	}
}

func TestGetAgentArgsSchema(t *testing.T) {
	s := schemaFor[GetAgentArgs](t)
	req := slices.Clone(s.Required)
	slices.Sort(req)
	require.Equal(t, []string{"agent_id", "org_slug"}, req)
	require.NotContains(t, s.Required, "detail_level")
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

func TestListSkillsArgsSchema(t *testing.T) {
	s := schemaFor[ListSkillsArgs](t)
	require.Empty(t, s.Required)
	require.Contains(t, s.Properties, "query")
}

func TestLoadSkillArgsSchema(t *testing.T) {
	req := sortedRequired[LoadSkillArgs](t)
	require.Equal(t, []string{"skill_name"}, req)
}
