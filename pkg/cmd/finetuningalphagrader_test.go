// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestFineTuningAlphaGradersRun(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"fine-tuning:alpha:graders", "run",
			"--grader", "{input: input, name: name, operation: eq, reference: reference, type: string_check}",
			"--model-sample", "model_sample",
			"--item", "{}",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"grader:\n" +
			"  input: input\n" +
			"  name: name\n" +
			"  operation: eq\n" +
			"  reference: reference\n" +
			"  type: string_check\n" +
			"model_sample: model_sample\n" +
			"item: {}\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"fine-tuning:alpha:graders", "run",
		)
	})
}

func TestFineTuningAlphaGradersValidate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"fine-tuning:alpha:graders", "validate",
			"--grader", "{input: input, name: name, operation: eq, reference: reference, type: string_check}",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"grader:\n" +
			"  input: input\n" +
			"  name: name\n" +
			"  operation: eq\n" +
			"  reference: reference\n" +
			"  type: string_check\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"fine-tuning:alpha:graders", "validate",
		)
	})
}
