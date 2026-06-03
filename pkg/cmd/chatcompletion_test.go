// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
)

func TestChatCompletionsCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"chat:completions", "create",
			"--max-items", "10",
			"--message", "{content: string, role: developer, name: name}",
			"--model", "gpt-5.4",
			"--audio", "{format: wav, voice: alloy}",
			"--frequency-penalty", "-2",
			"--function-call", "none",
			"--function", "{name: name, description: description, parameters: {foo: bar}}",
			"--logit-bias", "{foo: 0}",
			"--logprobs=true",
			"--max-completion-tokens", "0",
			"--max-tokens", "0",
			"--metadata", "{foo: string}",
			"--modality", "[text]",
			"--moderation", "{model: model}",
			"--n", "1",
			"--parallel-tool-calls=true",
			"--prediction", "{content: string, type: content}",
			"--presence-penalty", "-2",
			"--prompt-cache-key", "prompt-cache-key-1234",
			"--prompt-cache-retention", "in_memory",
			"--reasoning-effort", "none",
			"--response-format", "{type: text}",
			"--safety-identifier", "safety-identifier-1234",
			"--seed", "-9007199254740991",
			"--service-tier", "auto",
			"--stop", `"\n"`,
			"--store=true",
			"--stream=false",
			"--stream-options", "{include_obfuscation: true, include_usage: true}",
			"--temperature", "1",
			"--tool-choice", "none",
			"--tool", "{function: {name: name, description: description, parameters: {foo: bar}, strict: true}, type: function}",
			"--top-logprobs", "0",
			"--top-p", "1",
			"--user", "user-1234",
			"--verbosity", "low",
			"--web-search-options", "{search_context_size: low, user_location: {approximate: {city: city, country: country, region: region, timezone: timezone}, type: approximate}}",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(chatCompletionsCreate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"chat:completions", "create",
			"--max-items", "10",
			"--message", "{content: string, role: developer, name: name}",
			"--model", "gpt-5.4",
			"--audio.format", "wav",
			"--audio.voice", "alloy",
			"--frequency-penalty", "-2",
			"--function-call", "none",
			"--function.name", "name",
			"--function.description", "description",
			"--function.parameters", "{foo: bar}",
			"--logit-bias", "{foo: 0}",
			"--logprobs=true",
			"--max-completion-tokens", "0",
			"--max-tokens", "0",
			"--metadata", "{foo: string}",
			"--modality", "[text]",
			"--moderation.model", "model",
			"--n", "1",
			"--parallel-tool-calls=true",
			"--prediction.content", "string",
			"--prediction.type", "content",
			"--presence-penalty", "-2",
			"--prompt-cache-key", "prompt-cache-key-1234",
			"--prompt-cache-retention", "in_memory",
			"--reasoning-effort", "none",
			"--response-format", "{type: text}",
			"--safety-identifier", "safety-identifier-1234",
			"--seed", "-9007199254740991",
			"--service-tier", "auto",
			"--stop", `"\n"`,
			"--store=true",
			"--stream=false",
			"--stream-options.include-obfuscation=true",
			"--stream-options.include-usage=true",
			"--temperature", "1",
			"--tool-choice", "none",
			"--tool", "{function: {name: name, description: description, parameters: {foo: bar}, strict: true}, type: function}",
			"--top-logprobs", "0",
			"--top-p", "1",
			"--user", "user-1234",
			"--verbosity", "low",
			"--web-search-options.search-context-size", "low",
			"--web-search-options.user-location", "{approximate: {city: city, country: country, region: region, timezone: timezone}, type: approximate}",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"messages:\n" +
			"  - content: string\n" +
			"    role: developer\n" +
			"    name: name\n" +
			"model: gpt-5.4\n" +
			"audio:\n" +
			"  format: wav\n" +
			"  voice: alloy\n" +
			"frequency_penalty: -2\n" +
			"function_call: none\n" +
			"functions:\n" +
			"  - name: name\n" +
			"    description: description\n" +
			"    parameters:\n" +
			"      foo: bar\n" +
			"logit_bias:\n" +
			"  foo: 0\n" +
			"logprobs: true\n" +
			"max_completion_tokens: 0\n" +
			"max_tokens: 0\n" +
			"metadata:\n" +
			"  foo: string\n" +
			"modalities:\n" +
			"  - text\n" +
			"moderation:\n" +
			"  model: model\n" +
			"'n': 1\n" +
			"parallel_tool_calls: true\n" +
			"prediction:\n" +
			"  content: string\n" +
			"  type: content\n" +
			"presence_penalty: -2\n" +
			"prompt_cache_key: prompt-cache-key-1234\n" +
			"prompt_cache_retention: in_memory\n" +
			"reasoning_effort: none\n" +
			"response_format:\n" +
			"  type: text\n" +
			"safety_identifier: safety-identifier-1234\n" +
			"seed: -9007199254740991\n" +
			"service_tier: auto\n" +
			"stop: \"\\n\"\n" +
			"store: true\n" +
			"stream: false\n" +
			"stream_options:\n" +
			"  include_obfuscation: true\n" +
			"  include_usage: true\n" +
			"temperature: 1\n" +
			"tool_choice: none\n" +
			"tools:\n" +
			"  - function:\n" +
			"      name: name\n" +
			"      description: description\n" +
			"      parameters:\n" +
			"        foo: bar\n" +
			"      strict: true\n" +
			"    type: function\n" +
			"top_logprobs: 0\n" +
			"top_p: 1\n" +
			"user: user-1234\n" +
			"verbosity: low\n" +
			"web_search_options:\n" +
			"  search_context_size: low\n" +
			"  user_location:\n" +
			"    approximate:\n" +
			"      city: city\n" +
			"      country: country\n" +
			"      region: region\n" +
			"      timezone: timezone\n" +
			"    type: approximate\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"chat:completions", "create",
			"--max-items", "10",
		)
	})
}

func TestChatCompletionsRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"chat:completions", "retrieve",
			"--completion-id", "completion_id",
		)
	})
}

func TestChatCompletionsUpdate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"chat:completions", "update",
			"--completion-id", "completion_id",
			"--metadata", "{foo: string}",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"metadata:\n" +
			"  foo: string\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"chat:completions", "update",
			"--completion-id", "completion_id",
		)
	})
}

func TestChatCompletionsList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"chat:completions", "list",
			"--max-items", "10",
			"--after", "after",
			"--limit", "0",
			"--metadata", "{foo: string}",
			"--model", "model",
			"--order", "asc",
		)
	})
}

func TestChatCompletionsDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"chat:completions", "delete",
			"--completion-id", "completion_id",
		)
	})
}
