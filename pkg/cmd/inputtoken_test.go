// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
)

func TestResponsesInputTokensCount(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"responses:input-tokens", "count",
			"--conversation", "string",
			"--input", "string",
			"--instructions", "instructions",
			"--model", "model",
			"--parallel-tool-calls=true",
			"--personality", "friendly",
			"--previous-response-id", "resp_123",
			"--reasoning", "{context: auto, effort: none, generate_summary: auto, summary: auto}",
			"--text", "{format: {type: text}, verbosity: low}",
			"--tool-choice", "none",
			"--tool", "[{name: name, parameters: {foo: bar}, strict: true, type: function, defer_loading: true, description: description}]",
			"--truncation", "auto",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(responsesInputTokensCount)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"responses:input-tokens", "count",
			"--conversation", "string",
			"--input", "string",
			"--instructions", "instructions",
			"--model", "model",
			"--parallel-tool-calls=true",
			"--personality", "friendly",
			"--previous-response-id", "resp_123",
			"--reasoning.context", "auto",
			"--reasoning.effort", "none",
			"--reasoning.generate-summary", "auto",
			"--reasoning.summary", "auto",
			"--text.format", "{type: text}",
			"--text.verbosity", "low",
			"--tool-choice", "none",
			"--tool", "[{name: name, parameters: {foo: bar}, strict: true, type: function, defer_loading: true, description: description}]",
			"--truncation", "auto",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"conversation: string\n" +
			"input: string\n" +
			"instructions: instructions\n" +
			"model: model\n" +
			"parallel_tool_calls: true\n" +
			"personality: friendly\n" +
			"previous_response_id: resp_123\n" +
			"reasoning:\n" +
			"  context: auto\n" +
			"  effort: none\n" +
			"  generate_summary: auto\n" +
			"  summary: auto\n" +
			"text:\n" +
			"  format:\n" +
			"    type: text\n" +
			"  verbosity: low\n" +
			"tool_choice: none\n" +
			"tools:\n" +
			"  - name: name\n" +
			"    parameters:\n" +
			"      foo: bar\n" +
			"    strict: true\n" +
			"    type: function\n" +
			"    defer_loading: true\n" +
			"    description: description\n" +
			"truncation: auto\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"responses:input-tokens", "count",
		)
	})
}
