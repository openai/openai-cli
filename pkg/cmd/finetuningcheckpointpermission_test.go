// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestFineTuningCheckpointsPermissionsCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"fine-tuning:checkpoints:permissions", "create",
			"--max-items", "10",
			"--fine-tuned-model-checkpoint", "ft:gpt-4o-mini-2024-07-18:org:weather:B7R9VjQd",
			"--project-id", "string",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"project_ids:\n" +
			"  - string\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"fine-tuning:checkpoints:permissions", "create",
			"--max-items", "10",
			"--fine-tuned-model-checkpoint", "ft:gpt-4o-mini-2024-07-18:org:weather:B7R9VjQd",
		)
	})
}

func TestFineTuningCheckpointsPermissionsRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"fine-tuning:checkpoints:permissions", "retrieve",
			"--fine-tuned-model-checkpoint", "ft-AF1WoRqd3aJAHsqc9NY7iL8F",
			"--after", "after",
			"--limit", "0",
			"--order", "ascending",
			"--project-id", "project_id",
		)
	})
}

func TestFineTuningCheckpointsPermissionsList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"fine-tuning:checkpoints:permissions", "list",
			"--max-items", "10",
			"--fine-tuned-model-checkpoint", "ft-AF1WoRqd3aJAHsqc9NY7iL8F",
			"--after", "after",
			"--limit", "0",
			"--order", "ascending",
			"--project-id", "project_id",
		)
	})
}

func TestFineTuningCheckpointsPermissionsDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"fine-tuning:checkpoints:permissions", "delete",
			"--fine-tuned-model-checkpoint", "ft:gpt-4o-mini-2024-07-18:org:weather:B7R9VjQd",
			"--permission-id", "cp_zc4Q7MP6XxulcVzj4MZdwsAB",
		)
	})
}
