// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
)

func TestBetaAssistantsCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:assistants", "create",
			"--model", "gpt-4o",
			"--description", "description",
			"--instructions", "instructions",
			"--metadata", "{foo: string}",
			"--name", "name",
			"--reasoning-effort", "none",
			"--response-format", "auto",
			"--temperature", "1",
			"--tool-resources", "{code_interpreter: {file_ids: [string]}, file_search: {vector_store_ids: [string], vector_stores: [{chunking_strategy: {type: auto}, file_ids: [string], metadata: {foo: string}}]}}",
			"--tool", "{type: code_interpreter}",
			"--top-p", "1",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(betaAssistantsCreate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:assistants", "create",
			"--model", "gpt-4o",
			"--description", "description",
			"--instructions", "instructions",
			"--metadata", "{foo: string}",
			"--name", "name",
			"--reasoning-effort", "none",
			"--response-format", "auto",
			"--temperature", "1",
			"--tool-resources.code-interpreter", "{file_ids: [string]}",
			"--tool-resources.file-search", "{vector_store_ids: [string], vector_stores: [{chunking_strategy: {type: auto}, file_ids: [string], metadata: {foo: string}}]}",
			"--tool", "{type: code_interpreter}",
			"--top-p", "1",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"model: gpt-4o\n" +
			"description: description\n" +
			"instructions: instructions\n" +
			"metadata:\n" +
			"  foo: string\n" +
			"name: name\n" +
			"reasoning_effort: none\n" +
			"response_format: auto\n" +
			"temperature: 1\n" +
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
			"          foo: string\n" +
			"tools:\n" +
			"  - type: code_interpreter\n" +
			"top_p: 1\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:assistants", "create",
		)
	})
}

func TestBetaAssistantsRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:assistants", "retrieve",
			"--assistant-id", "assistant_id",
		)
	})
}

func TestBetaAssistantsUpdate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:assistants", "update",
			"--assistant-id", "assistant_id",
			"--description", "description",
			"--instructions", "instructions",
			"--metadata", "{foo: string}",
			"--model", "gpt-5",
			"--name", "name",
			"--reasoning-effort", "none",
			"--response-format", "auto",
			"--temperature", "1",
			"--tool-resources", "{code_interpreter: {file_ids: [string]}, file_search: {vector_store_ids: [string]}}",
			"--tool", "{type: code_interpreter}",
			"--top-p", "1",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(betaAssistantsUpdate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:assistants", "update",
			"--assistant-id", "assistant_id",
			"--description", "description",
			"--instructions", "instructions",
			"--metadata", "{foo: string}",
			"--model", "gpt-5",
			"--name", "name",
			"--reasoning-effort", "none",
			"--response-format", "auto",
			"--temperature", "1",
			"--tool-resources.code-interpreter", "{file_ids: [string]}",
			"--tool-resources.file-search", "{vector_store_ids: [string]}",
			"--tool", "{type: code_interpreter}",
			"--top-p", "1",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"description: description\n" +
			"instructions: instructions\n" +
			"metadata:\n" +
			"  foo: string\n" +
			"model: gpt-5\n" +
			"name: name\n" +
			"reasoning_effort: none\n" +
			"response_format: auto\n" +
			"temperature: 1\n" +
			"tool_resources:\n" +
			"  code_interpreter:\n" +
			"    file_ids:\n" +
			"      - string\n" +
			"  file_search:\n" +
			"    vector_store_ids:\n" +
			"      - string\n" +
			"tools:\n" +
			"  - type: code_interpreter\n" +
			"top_p: 1\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:assistants", "update",
			"--assistant-id", "assistant_id",
		)
	})
}

func TestBetaAssistantsList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:assistants", "list",
			"--max-items", "10",
			"--after", "after",
			"--before", "before",
			"--limit", "0",
			"--order", "asc",
		)
	})
}

func TestBetaAssistantsDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:assistants", "delete",
			"--assistant-id", "assistant_id",
		)
	})
}
