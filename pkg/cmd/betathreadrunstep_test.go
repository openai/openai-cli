// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestBetaThreadsRunsStepsRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads:runs:steps", "retrieve",
			"--thread-id", "thread_id",
			"--run-id", "run_id",
			"--step-id", "step_id",
			"--include", "step_details.tool_calls[*].file_search.results[*].content",
		)
	})
}

func TestBetaThreadsRunsStepsList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads:runs:steps", "list",
			"--max-items", "10",
			"--thread-id", "thread_id",
			"--run-id", "run_id",
			"--after", "after",
			"--before", "before",
			"--include", "step_details.tool_calls[*].file_search.results[*].content",
			"--limit", "0",
			"--order", "asc",
		)
	})
}
