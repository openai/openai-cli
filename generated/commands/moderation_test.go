// File generated from our OpenAPI spec by Castiron. See CONTRIBUTING.md for details.

package commands

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestModerationsCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"moderations", "create",
			"--input", `"...text to classify goes here..."`,
			"--model", "omni-moderation-2024-09-26",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"input: ...text to classify goes here...\n" +
			"model: omni-moderation-2024-09-26\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"moderations", "create",
		)
	})
}
