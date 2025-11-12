// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
)

func TestBetaThreadsMessagesCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads:messages", "create",
			"--thread-id", "thread_id",
			"--content", "string",
			"--role", "user",
			"--attachment", "[{file_id: file_id, tools: [{type: code_interpreter}]}]",
			"--metadata", "{foo: string}",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(betaThreadsMessagesCreate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads:messages", "create",
			"--thread-id", "thread_id",
			"--content", "string",
			"--role", "user",
			"--attachment.file-id", "file_id",
			"--attachment.tools", "[{type: code_interpreter}]",
			"--metadata", "{foo: string}",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"content: string\n" +
			"role: user\n" +
			"attachments:\n" +
			"  - file_id: file_id\n" +
			"    tools:\n" +
			"      - type: code_interpreter\n" +
			"metadata:\n" +
			"  foo: string\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads:messages", "create",
			"--thread-id", "thread_id",
		)
	})
}

func TestBetaThreadsMessagesRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads:messages", "retrieve",
			"--thread-id", "thread_id",
			"--message-id", "message_id",
		)
	})
}

func TestBetaThreadsMessagesUpdate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads:messages", "update",
			"--thread-id", "thread_id",
			"--message-id", "message_id",
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
			"beta:threads:messages", "update",
			"--thread-id", "thread_id",
			"--message-id", "message_id",
		)
	})
}

func TestBetaThreadsMessagesList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads:messages", "list",
			"--max-items", "10",
			"--thread-id", "thread_id",
			"--after", "after",
			"--before", "before",
			"--limit", "0",
			"--order", "asc",
			"--run-id", "run_id",
		)
	})
}

func TestBetaThreadsMessagesDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:threads:messages", "delete",
			"--thread-id", "thread_id",
			"--message-id", "message_id",
		)
	})
}
