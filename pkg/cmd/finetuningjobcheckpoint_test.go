// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestFineTuningJobsCheckpointsList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"fine-tuning:jobs:checkpoints", "list",
			"--max-items", "10",
			"--fine-tuning-job-id", "ft-AF1WoRqd3aJAHsqc9NY7iL8F",
			"--after", "after",
			"--limit", "0",
		)
	})
}
