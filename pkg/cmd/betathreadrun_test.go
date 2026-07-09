// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
)

func TestBetaThreadsRunsCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads:runs", "create",
			"--max-items", "10",
			"--thread-id", "thread_id",
			"--assistant-id", "assistant_id",
			"--include", "step_details.tool_calls[*].file_search.results[*].content",
			"--additional-instructions", "additional_instructions",
			"--additional-message", "[{content: string, role: user, attachments: [{file_id: file_id, tools: [{type: code_interpreter}]}], metadata: {foo: string}}]",
			"--instructions", "instructions",
			"--max-completion-tokens", "256",
			"--max-prompt-tokens", "256",
			"--metadata", "{foo: string}",
			"--model", "gpt-5.6-sol",
			"--parallel-tool-calls=true",
			"--reasoning-effort", "none",
			"--response-format", "auto",
			"--stream=false",
			"--temperature", "1",
			"--tool-choice", "none",
			"--tool", "[{type: code_interpreter}]",
			"--top-p", "1",
			"--truncation-strategy", "{type: auto, last_messages: 1}",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(betaThreadsRunsCreate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads:runs", "create",
			"--max-items", "10",
			"--thread-id", "thread_id",
			"--assistant-id", "assistant_id",
			"--include", "step_details.tool_calls[*].file_search.results[*].content",
			"--additional-instructions", "additional_instructions",
			"--additional-message.content", "string",
			"--additional-message.role", "user",
			"--additional-message.attachments", "[{file_id: file_id, tools: [{type: code_interpreter}]}]",
			"--additional-message.metadata", "{foo: string}",
			"--instructions", "instructions",
			"--max-completion-tokens", "256",
			"--max-prompt-tokens", "256",
			"--metadata", "{foo: string}",
			"--model", "gpt-5.6-sol",
			"--parallel-tool-calls=true",
			"--reasoning-effort", "none",
			"--response-format", "auto",
			"--stream=false",
			"--temperature", "1",
			"--tool-choice", "none",
			"--tool", "[{type: code_interpreter}]",
			"--top-p", "1",
			"--truncation-strategy.type", "auto",
			"--truncation-strategy.last-messages", "1",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"assistant_id: assistant_id\n" +
			"additional_instructions: additional_instructions\n" +
			"additional_messages:\n" +
			"  - content: string\n" +
			"    role: user\n" +
			"    attachments:\n" +
			"      - file_id: file_id\n" +
			"        tools:\n" +
			"          - type: code_interpreter\n" +
			"    metadata:\n" +
			"      foo: string\n" +
			"instructions: instructions\n" +
			"max_completion_tokens: 256\n" +
			"max_prompt_tokens: 256\n" +
			"metadata:\n" +
			"  foo: string\n" +
			"model: gpt-5.6-sol\n" +
			"parallel_tool_calls: true\n" +
			"reasoning_effort: none\n" +
			"response_format: auto\n" +
			"stream: false\n" +
			"temperature: 1\n" +
			"tool_choice: none\n" +
			"tools:\n" +
			"  - type: code_interpreter\n" +
			"top_p: 1\n" +
			"truncation_strategy:\n" +
			"  type: auto\n" +
			"  last_messages: 1\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads:runs", "create",
			"--max-items", "10",
			"--thread-id", "thread_id",
			"--include", "step_details.tool_calls[*].file_search.results[*].content",
		)
	})
}

func TestBetaThreadsRunsRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads:runs", "retrieve",
			"--thread-id", "thread_id",
			"--run-id", "run_id",
		)
	})
}

func TestBetaThreadsRunsUpdate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads:runs", "update",
			"--thread-id", "thread_id",
			"--run-id", "run_id",
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
			"beta:threads:runs", "update",
			"--thread-id", "thread_id",
			"--run-id", "run_id",
		)
	})
}

func TestBetaThreadsRunsList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads:runs", "list",
			"--max-items", "10",
			"--thread-id", "thread_id",
			"--after", "after",
			"--before", "before",
			"--limit", "0",
			"--order", "asc",
		)
	})
}

func TestBetaThreadsRunsCancel(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads:runs", "cancel",
			"--thread-id", "thread_id",
			"--run-id", "run_id",
		)
	})
}

func TestBetaThreadsRunsSubmitToolOutputs(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads:runs", "submit-tool-outputs",
			"--max-items", "10",
			"--thread-id", "thread_id",
			"--run-id", "run_id",
			"--tool-output", "{output: output, tool_call_id: tool_call_id}",
			"--stream=false",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(betaThreadsRunsSubmitToolOutputs)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads:runs", "submit-tool-outputs",
			"--max-items", "10",
			"--thread-id", "thread_id",
			"--run-id", "run_id",
			"--tool-output.output", "output",
			"--tool-output.tool-call-id", "tool_call_id",
			"--stream=false",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"tool_outputs:\n" +
			"  - output: output\n" +
			"    tool_call_id: tool_call_id\n" +
			"stream: false\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads:runs", "submit-tool-outputs",
			"--max-items", "10",
			"--thread-id", "thread_id",
			"--run-id", "run_id",
		)
	})
}
