// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
)

func TestCompletionsCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"completions", "create",
			"--max-items", "10",
			"--model", "gpt-3.5-turbo-instruct",
			"--prompt", "This is a test.",
			"--best-of", "0",
			"--echo=true",
			"--frequency-penalty", "-2",
			"--logit-bias", "{foo: 0}",
			"--logprobs", "0",
			"--max-tokens", "16",
			"--n", "1",
			"--presence-penalty", "-2",
			"--seed", "0",
			"--stop", `"\n"`,
			"--stream=false",
			"--stream-options", "{include_obfuscation: true, include_usage: true}",
			"--suffix", "test.",
			"--temperature", "1",
			"--top-p", "1",
			"--user", "user-1234",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(completionsCreate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"completions", "create",
			"--max-items", "10",
			"--model", "gpt-3.5-turbo-instruct",
			"--prompt", "This is a test.",
			"--best-of", "0",
			"--echo=true",
			"--frequency-penalty", "-2",
			"--logit-bias", "{foo: 0}",
			"--logprobs", "0",
			"--max-tokens", "16",
			"--n", "1",
			"--presence-penalty", "-2",
			"--seed", "0",
			"--stop", `"\n"`,
			"--stream=false",
			"--stream-options.include-obfuscation=true",
			"--stream-options.include-usage=true",
			"--suffix", "test.",
			"--temperature", "1",
			"--top-p", "1",
			"--user", "user-1234",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"model: gpt-3.5-turbo-instruct\n" +
			"prompt: This is a test.\n" +
			"best_of: 0\n" +
			"echo: true\n" +
			"frequency_penalty: -2\n" +
			"logit_bias:\n" +
			"  foo: 0\n" +
			"logprobs: 0\n" +
			"max_tokens: 16\n" +
			"'n': 1\n" +
			"presence_penalty: -2\n" +
			"seed: 0\n" +
			"stop: \"\\n\"\n" +
			"stream: false\n" +
			"stream_options:\n" +
			"  include_obfuscation: true\n" +
			"  include_usage: true\n" +
			"suffix: test.\n" +
			"temperature: 1\n" +
			"top_p: 1\n" +
			"user: user-1234\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"completions", "create",
			"--max-items", "10",
		)
	})
}
