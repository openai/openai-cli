// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
)

func TestBetaThreadsCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads", "create",
			"--message", "{content: string, role: user, attachments: [{file_id: file_id, tools: [{type: code_interpreter}]}], metadata: {foo: string}}",
			"--metadata", "{foo: string}",
			"--tool-resources", "{code_interpreter: {file_ids: [string]}, file_search: {vector_store_ids: [string], vector_stores: [{chunking_strategy: {type: auto}, file_ids: [string], metadata: {foo: string}}]}}",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(betaThreadsCreate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads", "create",
			"--message.content", "string",
			"--message.role", "user",
			"--message.attachments", "[{file_id: file_id, tools: [{type: code_interpreter}]}]",
			"--message.metadata", "{foo: string}",
			"--metadata", "{foo: string}",
			"--tool-resources.code-interpreter", "{file_ids: [string]}",
			"--tool-resources.file-search", "{vector_store_ids: [string], vector_stores: [{chunking_strategy: {type: auto}, file_ids: [string], metadata: {foo: string}}]}",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"messages:\n" +
			"  - content: string\n" +
			"    role: user\n" +
			"    attachments:\n" +
			"      - file_id: file_id\n" +
			"        tools:\n" +
			"          - type: code_interpreter\n" +
			"    metadata:\n" +
			"      foo: string\n" +
			"metadata:\n" +
			"  foo: string\n" +
			"tool_resources:\n" +
			"  code_interpreter:\n" +
			"    file_ids:\n" +
			"      - string\n" +
			"  file_search:\n" +
			"    vector_store_ids:\n" +
			"      - string\n" +
			"    vector_stores:\n" +
			"      - chunking_strategy:\n" +
			"          type: auto\n" +
			"        file_ids:\n" +
			"          - string\n" +
			"        metadata:\n" +
			"          foo: string\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads", "create",
		)
	})
}

func TestBetaThreadsRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads", "retrieve",
			"--thread-id", "thread_id",
		)
	})
}

func TestBetaThreadsUpdate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads", "update",
			"--thread-id", "thread_id",
			"--metadata", "{foo: string}",
			"--tool-resources", "{code_interpreter: {file_ids: [string]}, file_search: {vector_store_ids: [string]}}",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(betaThreadsUpdate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads", "update",
			"--thread-id", "thread_id",
			"--metadata", "{foo: string}",
			"--tool-resources.code-interpreter", "{file_ids: [string]}",
			"--tool-resources.file-search", "{vector_store_ids: [string]}",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"metadata:\n" +
			"  foo: string\n" +
			"tool_resources:\n" +
			"  code_interpreter:\n" +
			"    file_ids:\n" +
			"      - string\n" +
			"  file_search:\n" +
			"    vector_store_ids:\n" +
			"      - string\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads", "update",
			"--thread-id", "thread_id",
		)
	})
}

func TestBetaThreadsDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads", "delete",
			"--thread-id", "thread_id",
		)
	})
}

func TestBetaThreadsCreateAndRun(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads", "create-and-run",
			"--max-items", "10",
			"--assistant-id", "assistant_id",
			"--instructions", "instructions",
			"--max-completion-tokens", "256",
			"--max-prompt-tokens", "256",
			"--metadata", "{foo: string}",
			"--model", "gpt-5.4",
			"--parallel-tool-calls=true",
			"--response-format", "auto",
			"--stream=false",
			"--temperature", "1",
			"--thread", "{messages: [{content: string, role: user, attachments: [{file_id: file_id, tools: [{type: code_interpreter}]}], metadata: {foo: string}}], metadata: {foo: string}, tool_resources: {code_interpreter: {file_ids: [string]}, file_search: {vector_store_ids: [string], vector_stores: [{chunking_strategy: {type: auto}, file_ids: [string], metadata: {foo: string}}]}}}",
			"--tool-choice", "none",
			"--tool-resources", "{code_interpreter: {file_ids: [string]}, file_search: {vector_store_ids: [string]}}",
			"--tool", "[{type: code_interpreter}]",
			"--top-p", "1",
			"--truncation-strategy", "{type: auto, last_messages: 1}",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(betaThreadsCreateAndRun)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads", "create-and-run",
			"--max-items", "10",
			"--assistant-id", "assistant_id",
			"--instructions", "instructions",
			"--max-completion-tokens", "256",
			"--max-prompt-tokens", "256",
			"--metadata", "{foo: string}",
			"--model", "gpt-5.4",
			"--parallel-tool-calls=true",
			"--response-format", "auto",
			"--stream=false",
			"--temperature", "1",
			"--thread.messages", "[{content: string, role: user, attachments: [{file_id: file_id, tools: [{type: code_interpreter}]}], metadata: {foo: string}}]",
			"--thread.metadata", "{foo: string}",
			"--thread.tool-resources", "{code_interpreter: {file_ids: [string]}, file_search: {vector_store_ids: [string], vector_stores: [{chunking_strategy: {type: auto}, file_ids: [string], metadata: {foo: string}}]}}",
			"--tool-choice", "none",
			"--tool-resources.code-interpreter", "{file_ids: [string]}",
			"--tool-resources.file-search", "{vector_store_ids: [string]}",
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
			"instructions: instructions\n" +
			"max_completion_tokens: 256\n" +
			"max_prompt_tokens: 256\n" +
			"metadata:\n" +
			"  foo: string\n" +
			"model: gpt-5.4\n" +
			"parallel_tool_calls: true\n" +
			"response_format: auto\n" +
			"stream: false\n" +
			"temperature: 1\n" +
			"thread:\n" +
			"  messages:\n" +
			"    - content: string\n" +
			"      role: user\n" +
			"      attachments:\n" +
			"        - file_id: file_id\n" +
			"          tools:\n" +
			"            - type: code_interpreter\n" +
			"      metadata:\n" +
			"        foo: string\n" +
			"  metadata:\n" +
			"    foo: string\n" +
			"  tool_resources:\n" +
			"    code_interpreter:\n" +
			"      file_ids:\n" +
			"        - string\n" +
			"    file_search:\n" +
			"      vector_store_ids:\n" +
			"        - string\n" +
			"      vector_stores:\n" +
			"        - chunking_strategy:\n" +
			"            type: auto\n" +
			"          file_ids:\n" +
			"            - string\n" +
			"          metadata:\n" +
			"            foo: string\n" +
			"tool_choice: none\n" +
			"tool_resources:\n" +
			"  code_interpreter:\n" +
			"    file_ids:\n" +
			"      - string\n" +
			"  file_search:\n" +
			"    vector_store_ids:\n" +
			"      - string\n" +
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
			"beta:threads", "create-and-run",
			"--max-items", "10",
		)
	})
}
