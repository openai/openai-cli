// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestChatCompletionsMessagesList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"chat:completions:messages", "list",
			"--max-items", "10",
			"--completion-id", "completion_id",
			"--after", "after",
			"--limit", "0",
			"--order", "asc",
		)
	})
}
