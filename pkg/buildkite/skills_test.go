package buildkite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListSkills(t *testing.T) {
	ctx := context.Background()

	t.Run("no query returns all skills", func(t *testing.T) {
		assert := require.New(t)

		tool, handler, _ := ListSkills()
		assert.NotNil(tool)
		assert.NotNil(handler)

		request := createMCPRequest(t, map[string]any{})
		result, _, err := handler(ctx, request, ListSkillsArgs{})
		assert.NoError(err)

		textContent := getTextResult(t, result)
		assert.Contains(textContent.Text, "debug-logs-guide")
	})

	t.Run("query filters by substring case-insensitively", func(t *testing.T) {
		assert := require.New(t)

		_, handler, _ := ListSkills()
		request := createMCPRequest(t, map[string]any{"query": "DEBUG"})
		result, _, err := handler(ctx, request, ListSkillsArgs{Query: "DEBUG"})
		assert.NoError(err)

		textContent := getTextResult(t, result)
		assert.Contains(textContent.Text, "debug-logs-guide")
	})

	t.Run("query matching nothing returns empty list", func(t *testing.T) {
		assert := require.New(t)

		_, handler, _ := ListSkills()
		request := createMCPRequest(t, map[string]any{"query": "nonexistent-skill-xyz"})
		result, _, err := handler(ctx, request, ListSkillsArgs{Query: "nonexistent-skill-xyz"})
		assert.NoError(err)

		textContent := getTextResult(t, result)
		assert.JSONEq(`[]`, textContent.Text)
	})

	t.Run("multi-word query matches when all tokens appear out of order", func(t *testing.T) {
		assert := require.New(t)

		_, handler, _ := ListSkills()
		request := createMCPRequest(t, map[string]any{"query": "debug build failure"})
		result, _, err := handler(ctx, request, ListSkillsArgs{Query: "debug build failure"})
		assert.NoError(err)

		textContent := getTextResult(t, result)
		assert.Contains(textContent.Text, "debug-logs-guide")
	})

	t.Run("multi-word query with one non-matching token returns empty list", func(t *testing.T) {
		assert := require.New(t)

		_, handler, _ := ListSkills()
		request := createMCPRequest(t, map[string]any{"query": "debug nonexistent"})
		result, _, err := handler(ctx, request, ListSkillsArgs{Query: "debug nonexistent"})
		assert.NoError(err)

		textContent := getTextResult(t, result)
		assert.JSONEq(`[]`, textContent.Text)
	})
}

func TestLoadSkill(t *testing.T) {
	ctx := context.Background()

	t.Run("known name returns file content", func(t *testing.T) {
		assert := require.New(t)

		tool, handler, _ := LoadSkill()
		assert.NotNil(tool)
		assert.NotNil(handler)

		request := createMCPRequest(t, map[string]any{"skill_name": "debug-logs-guide"})
		result, _, err := handler(ctx, request, LoadSkillArgs{SkillName: "debug-logs-guide"})
		assert.NoError(err)
		assert.False(result.IsError)

		textContent := getTextResult(t, result)
		assert.Contains(textContent.Text, "broken")
	})

	t.Run("unknown name returns error mentioning valid names", func(t *testing.T) {
		assert := require.New(t)

		_, handler, _ := LoadSkill()
		request := createMCPRequest(t, map[string]any{"skill_name": "nonexistent-skill"})
		result, _, err := handler(ctx, request, LoadSkillArgs{SkillName: "nonexistent-skill"})
		assert.NoError(err)
		assert.True(result.IsError)

		textContent := getTextResult(t, result)
		assert.Contains(textContent.Text, "debug-logs-guide")
	})
}
