// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestSkillsVersionsCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"skills:versions", "create",
			"--skill-id", "skill_123",
			"--default=true",
			"--files", mocktest.TestFile(t, "[Example data]"),
		)
	})

	t.Run("piping data", func(t *testing.T) {
		testFile := mocktest.TestFile(t, "Example data")
		// Test piping YAML data over stdin
		pipeDataStr := "" +
			"default: true\n" +
			"files:\n" +
			"  - Example data\n"
		pipeDataStr = strings.ReplaceAll(pipeDataStr, "Example data", testFile)
		pipeData := []byte(pipeDataStr)
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"skills:versions", "create",
			"--skill-id", "skill_123",
		)
	})
}

func TestSkillsVersionsRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"skills:versions", "retrieve",
			"--skill-id", "skill_123",
			"--version", "version",
		)
	})
}

func TestSkillsVersionsList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"skills:versions", "list",
			"--max-items", "10",
			"--skill-id", "skill_123",
			"--after", "skillver_123",
			"--limit", "0",
			"--order", "asc",
		)
	})
}

func TestSkillsVersionsDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"skills:versions", "delete",
			"--skill-id", "skill_123",
			"--version", "version",
		)
	})
}
