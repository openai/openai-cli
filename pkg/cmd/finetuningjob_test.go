// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
)

func TestFineTuningJobsCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"fine-tuning:jobs", "create",
			"--model", "gpt-4o-mini",
			"--training-file", "file-abc123",
			"--hyperparameters", "{batch_size: auto, learning_rate_multiplier: auto, n_epochs: auto}",
			"--integration", "[{type: wandb, wandb: {project: my-wandb-project, entity: entity, name: name, tags: [custom-tag]}}]",
			"--metadata", "{foo: string}",
			"--method", "{type: supervised, dpo: {hyperparameters: {batch_size: auto, beta: auto, learning_rate_multiplier: auto, n_epochs: auto}}, reinforcement: {grader: {input: input, name: name, operation: eq, reference: reference, type: string_check}, hyperparameters: {batch_size: auto, compute_multiplier: auto, eval_interval: auto, eval_samples: auto, learning_rate_multiplier: auto, n_epochs: auto, reasoning_effort: default}}, supervised: {hyperparameters: {batch_size: auto, learning_rate_multiplier: auto, n_epochs: auto}}}",
			"--seed", "42",
			"--suffix", "x",
			"--validation-file", "file-abc123",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(fineTuningJobsCreate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"fine-tuning:jobs", "create",
			"--model", "gpt-4o-mini",
			"--training-file", "file-abc123",
			"--hyperparameters.batch-size", "auto",
			"--hyperparameters.learning-rate-multiplier", "auto",
			"--hyperparameters.n-epochs", "auto",
			"--integration.type", "wandb",
			"--integration.wandb", "{project: my-wandb-project, entity: entity, name: name, tags: [custom-tag]}",
			"--metadata", "{foo: string}",
			"--method.type", "supervised",
			"--method.dpo", "{hyperparameters: {batch_size: auto, beta: auto, learning_rate_multiplier: auto, n_epochs: auto}}",
			"--method.reinforcement", "{grader: {input: input, name: name, operation: eq, reference: reference, type: string_check}, hyperparameters: {batch_size: auto, compute_multiplier: auto, eval_interval: auto, eval_samples: auto, learning_rate_multiplier: auto, n_epochs: auto, reasoning_effort: default}}",
			"--method.supervised", "{hyperparameters: {batch_size: auto, learning_rate_multiplier: auto, n_epochs: auto}}",
			"--seed", "42",
			"--suffix", "x",
			"--validation-file", "file-abc123",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"model: gpt-4o-mini\n" +
			"training_file: file-abc123\n" +
			"hyperparameters:\n" +
			"  batch_size: auto\n" +
			"  learning_rate_multiplier: auto\n" +
			"  n_epochs: auto\n" +
			"integrations:\n" +
			"  - type: wandb\n" +
			"    wandb:\n" +
			"      project: my-wandb-project\n" +
			"      entity: entity\n" +
			"      name: name\n" +
			"      tags:\n" +
			"        - custom-tag\n" +
			"metadata:\n" +
			"  foo: string\n" +
			"method:\n" +
			"  type: supervised\n" +
			"  dpo:\n" +
			"    hyperparameters:\n" +
			"      batch_size: auto\n" +
			"      beta: auto\n" +
			"      learning_rate_multiplier: auto\n" +
			"      n_epochs: auto\n" +
			"  reinforcement:\n" +
			"    grader:\n" +
			"      input: input\n" +
			"      name: name\n" +
			"      operation: eq\n" +
			"      reference: reference\n" +
			"      type: string_check\n" +
			"    hyperparameters:\n" +
			"      batch_size: auto\n" +
			"      compute_multiplier: auto\n" +
			"      eval_interval: auto\n" +
			"      eval_samples: auto\n" +
			"      learning_rate_multiplier: auto\n" +
			"      n_epochs: auto\n" +
			"      reasoning_effort: default\n" +
			"  supervised:\n" +
			"    hyperparameters:\n" +
			"      batch_size: auto\n" +
			"      learning_rate_multiplier: auto\n" +
			"      n_epochs: auto\n" +
			"seed: 42\n" +
			"suffix: x\n" +
			"validation_file: file-abc123\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"fine-tuning:jobs", "create",
		)
	})
}

func TestFineTuningJobsRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"fine-tuning:jobs", "retrieve",
			"--fine-tuning-job-id", "ft-AF1WoRqd3aJAHsqc9NY7iL8F",
		)
	})
}

func TestFineTuningJobsList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"fine-tuning:jobs", "list",
			"--max-items", "10",
			"--after", "after",
			"--limit", "0",
			"--metadata", "{foo: string}",
		)
	})
}

func TestFineTuningJobsCancel(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"fine-tuning:jobs", "cancel",
			"--fine-tuning-job-id", "ft-AF1WoRqd3aJAHsqc9NY7iL8F",
		)
	})
}

func TestFineTuningJobsListEvents(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"fine-tuning:jobs", "list-events",
			"--max-items", "10",
			"--fine-tuning-job-id", "ft-AF1WoRqd3aJAHsqc9NY7iL8F",
			"--after", "after",
			"--limit", "0",
		)
	})
}

func TestFineTuningJobsPause(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"fine-tuning:jobs", "pause",
			"--fine-tuning-job-id", "ft-AF1WoRqd3aJAHsqc9NY7iL8F",
		)
	})
}

func TestFineTuningJobsResume(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"fine-tuning:jobs", "resume",
			"--fine-tuning-job-id", "ft-AF1WoRqd3aJAHsqc9NY7iL8F",
		)
	})
}
