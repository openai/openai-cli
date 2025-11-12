// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
)

func TestResponsesCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"responses", "create",
			"--max-items", "10",
			"--background=true",
			"--context-management", "[{type: type, compact_threshold: 1000}]",
			"--conversation", "string",
			"--include", "[file_search_call.results]",
			"--input", "string",
			"--instructions", "instructions",
			"--max-output-tokens", "16",
			"--max-tool-calls", "0",
			"--metadata", "{foo: string}",
			"--model", "gpt-5.1",
			"--parallel-tool-calls=true",
			"--previous-response-id", "previous_response_id",
			"--prompt", "{id: id, variables: {foo: string}, version: version}",
			"--prompt-cache-key", "prompt-cache-key-1234",
			"--prompt-cache-retention", "in_memory",
			"--reasoning", "{effort: none, generate_summary: auto, summary: auto}",
			"--safety-identifier", "safety-identifier-1234",
			"--service-tier", "auto",
			"--store=true",
			"--stream=false",
			"--stream-options", "{include_obfuscation: true}",
			"--temperature", "1",
			"--text", "{format: {type: text}, verbosity: low}",
			"--tool-choice", "none",
			"--tool", "{name: name, parameters: {foo: bar}, strict: true, type: function, defer_loading: true, description: description}",
			"--top-logprobs", "0",
			"--top-p", "1",
			"--truncation", "auto",
			"--user", "user-1234",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(responsesCreate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"responses", "create",
			"--max-items", "10",
			"--background=true",
			"--context-management.type", "type",
			"--context-management.compact-threshold", "1000",
			"--conversation", "string",
			"--include", "[file_search_call.results]",
			"--input", "string",
			"--instructions", "instructions",
			"--max-output-tokens", "16",
			"--max-tool-calls", "0",
			"--metadata", "{foo: string}",
			"--model", "gpt-5.1",
			"--parallel-tool-calls=true",
			"--previous-response-id", "previous_response_id",
			"--prompt.id", "id",
			"--prompt.variables", "{foo: string}",
			"--prompt.version", "version",
			"--prompt-cache-key", "prompt-cache-key-1234",
			"--prompt-cache-retention", "in_memory",
			"--reasoning.effort", "none",
			"--reasoning.generate-summary", "auto",
			"--reasoning.summary", "auto",
			"--safety-identifier", "safety-identifier-1234",
			"--service-tier", "auto",
			"--store=true",
			"--stream=false",
			"--stream-options.include-obfuscation=true",
			"--temperature", "1",
			"--text.format", "{type: text}",
			"--text.verbosity", "low",
			"--tool-choice", "none",
			"--tool", "{name: name, parameters: {foo: bar}, strict: true, type: function, defer_loading: true, description: description}",
			"--top-logprobs", "0",
			"--top-p", "1",
			"--truncation", "auto",
			"--user", "user-1234",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"background: true\n" +
			"context_management:\n" +
			"  - type: type\n" +
			"    compact_threshold: 1000\n" +
			"conversation: string\n" +
			"include:\n" +
			"  - file_search_call.results\n" +
			"input: string\n" +
			"instructions: instructions\n" +
			"max_output_tokens: 16\n" +
			"max_tool_calls: 0\n" +
			"metadata:\n" +
			"  foo: string\n" +
			"model: gpt-5.1\n" +
			"parallel_tool_calls: true\n" +
			"previous_response_id: previous_response_id\n" +
			"prompt:\n" +
			"  id: id\n" +
			"  variables:\n" +
			"    foo: string\n" +
			"  version: version\n" +
			"prompt_cache_key: prompt-cache-key-1234\n" +
			"prompt_cache_retention: in_memory\n" +
			"reasoning:\n" +
			"  effort: none\n" +
			"  generate_summary: auto\n" +
			"  summary: auto\n" +
			"safety_identifier: safety-identifier-1234\n" +
			"service_tier: auto\n" +
			"store: true\n" +
			"stream: false\n" +
			"stream_options:\n" +
			"  include_obfuscation: true\n" +
			"temperature: 1\n" +
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
			"top_logprobs: 0\n" +
			"top_p: 1\n" +
			"truncation: auto\n" +
			"user: user-1234\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"responses", "create",
			"--max-items", "10",
		)
	})
}

func TestResponsesRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"responses", "retrieve",
			"--max-items", "10",
			"--response-id", "resp_677efb5139a88190b512bc3fef8e535d",
			"--include", "file_search_call.results",
			"--include-obfuscation=true",
			"--starting-after", "0",
			"--stream=false",
		)
	})
}

func TestResponsesDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"responses", "delete",
			"--response-id", "resp_677efb5139a88190b512bc3fef8e535d",
		)
	})
}

func TestResponsesCancel(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"responses", "cancel",
			"--response-id", "resp_677efb5139a88190b512bc3fef8e535d",
		)
	})
}

func TestResponsesCompact(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"responses", "compact",
			"--model", "gpt-5.4",
			"--input", "string",
			"--instructions", "instructions",
			"--previous-response-id", "resp_123",
			"--prompt-cache-key", "prompt_cache_key",
			"--prompt-cache-retention", "in_memory",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"model: gpt-5.4\n" +
			"input: string\n" +
			"instructions: instructions\n" +
			"previous_response_id: resp_123\n" +
			"prompt_cache_key: prompt_cache_key\n" +
			"prompt_cache_retention: in_memory\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"responses", "compact",
		)
	})
}
