// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
)

func TestBetaResponsesCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:responses", "create",
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
			"--moderation", "{model: model, policy: {input: {mode: score}, output: {mode: score}}}",
			"--multi-agent", "{enabled: true, max_concurrent_subagents: 1}",
			"--parallel-tool-calls=true",
			"--previous-response-id", "previous_response_id",
			"--prompt", "{id: id, variables: {foo: string}, version: version}",
			"--prompt-cache-key", "prompt-cache-key-1234",
			"--prompt-cache-options", "{mode: implicit, ttl: 30m}",
			"--prompt-cache-retention", "in_memory",
			"--reasoning", "{context: auto, effort: none, generate_summary: auto, mode: standard, summary: auto}",
			"--safety-identifier", "safety-identifier-1234",
			"--service-tier", "auto",
			"--store=true",
			"--stream=false",
			"--stream-options", "{include_obfuscation: true}",
			"--temperature", "1",
			"--text", "{format: {type: text}, verbosity: low}",
			"--tool-choice", "none",
			"--tool", "{name: name, parameters: {foo: bar}, strict: true, type: function, allowed_callers: [direct], defer_loading: true, description: description, output_schema: {foo: bar}}",
			"--top-logprobs", "0",
			"--top-p", "1",
			"--truncation", "auto",
			"--user", "user-1234",
			"--beta", "responses_multi_agent=v1",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(betaResponsesCreate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:responses", "create",
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
			"--moderation.model", "model",
			"--moderation.policy", "{input: {mode: score}, output: {mode: score}}",
			"--multi-agent.enabled=true",
			"--multi-agent.max-concurrent-subagents", "1",
			"--parallel-tool-calls=true",
			"--previous-response-id", "previous_response_id",
			"--prompt.id", "id",
			"--prompt.variables", "{foo: string}",
			"--prompt.version", "version",
			"--prompt-cache-key", "prompt-cache-key-1234",
			"--prompt-cache-options.mode", "implicit",
			"--prompt-cache-options.ttl", "30m",
			"--prompt-cache-retention", "in_memory",
			"--reasoning.context", "auto",
			"--reasoning.effort", "none",
			"--reasoning.generate-summary", "auto",
			"--reasoning.mode", "standard",
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
			"--tool", "{name: name, parameters: {foo: bar}, strict: true, type: function, allowed_callers: [direct], defer_loading: true, description: description, output_schema: {foo: bar}}",
			"--top-logprobs", "0",
			"--top-p", "1",
			"--truncation", "auto",
			"--user", "user-1234",
			"--beta", "responses_multi_agent=v1",
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
			"moderation:\n" +
			"  model: model\n" +
			"  policy:\n" +
			"    input:\n" +
			"      mode: score\n" +
			"    output:\n" +
			"      mode: score\n" +
			"multi_agent:\n" +
			"  enabled: true\n" +
			"  max_concurrent_subagents: 1\n" +
			"parallel_tool_calls: true\n" +
			"previous_response_id: previous_response_id\n" +
			"prompt:\n" +
			"  id: id\n" +
			"  variables:\n" +
			"    foo: string\n" +
			"  version: version\n" +
			"prompt_cache_key: prompt-cache-key-1234\n" +
			"prompt_cache_options:\n" +
			"  mode: implicit\n" +
			"  ttl: 30m\n" +
			"prompt_cache_retention: in_memory\n" +
			"reasoning:\n" +
			"  context: auto\n" +
			"  effort: none\n" +
			"  generate_summary: auto\n" +
			"  mode: standard\n" +
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
			"    allowed_callers:\n" +
			"      - direct\n" +
			"    defer_loading: true\n" +
			"    description: description\n" +
			"    output_schema:\n" +
			"      foo: bar\n" +
			"top_logprobs: 0\n" +
			"top_p: 1\n" +
			"truncation: auto\n" +
			"user: user-1234\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:responses", "create",
			"--max-items", "10",
			"--beta", "responses_multi_agent=v1",
		)
	})
}

func TestBetaResponsesRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:responses", "retrieve",
			"--max-items", "10",
			"--response-id", "resp_677efb5139a88190b512bc3fef8e535d",
			"--include", "file_search_call.results",
			"--include-obfuscation=true",
			"--starting-after", "0",
			"--stream=false",
			"--beta", "responses_multi_agent=v1",
		)
	})
}

func TestBetaResponsesDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:responses", "delete",
			"--response-id", "resp_677efb5139a88190b512bc3fef8e535d",
			"--beta", "responses_multi_agent=v1",
		)
	})
}

func TestBetaResponsesCancel(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:responses", "cancel",
			"--response-id", "resp_677efb5139a88190b512bc3fef8e535d",
			"--beta", "responses_multi_agent=v1",
		)
	})
}

func TestBetaResponsesCompact(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:responses", "compact",
			"--model", "gpt-5.6-sol",
			"--input", "string",
			"--instructions", "instructions",
			"--previous-response-id", "resp_123",
			"--prompt-cache-key", "prompt_cache_key",
			"--prompt-cache-options", "{mode: implicit, ttl: 30m}",
			"--prompt-cache-retention", "in_memory",
			"--service-tier", "auto",
			"--beta", "responses_multi_agent=v1",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(betaResponsesCompact)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:responses", "compact",
			"--model", "gpt-5.6-sol",
			"--input", "string",
			"--instructions", "instructions",
			"--previous-response-id", "resp_123",
			"--prompt-cache-key", "prompt_cache_key",
			"--prompt-cache-options.mode", "implicit",
			"--prompt-cache-options.ttl", "30m",
			"--prompt-cache-retention", "in_memory",
			"--service-tier", "auto",
			"--beta", "responses_multi_agent=v1",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"model: gpt-5.6-sol\n" +
			"input: string\n" +
			"instructions: instructions\n" +
			"previous_response_id: resp_123\n" +
			"prompt_cache_key: prompt_cache_key\n" +
			"prompt_cache_options:\n" +
			"  mode: implicit\n" +
			"  ttl: 30m\n" +
			"prompt_cache_retention: in_memory\n" +
			"service_tier: auto\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:responses", "compact",
			"--beta", "responses_multi_agent=v1",
		)
	})
}
