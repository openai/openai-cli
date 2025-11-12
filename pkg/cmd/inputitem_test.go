// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestResponsesInputItemsList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"responses:input-items", "list",
			"--max-items", "10",
			"--response-id", "response_id",
			"--after", "after",
			"--include", "file_search_call.results",
			"--limit", "0",
			"--order", "asc",
		)
	})
}
