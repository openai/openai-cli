// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestConversationsItemsCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"conversations:items", "create",
			"--conversation-id", "conv_123",
			"--item", "{content: string, role: user, phase: commentary, type: message}",
			"--include", "file_search_call.results",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"items:\n" +
			"  - content: string\n" +
			"    role: user\n" +
			"    phase: commentary\n" +
			"    type: message\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"conversations:items", "create",
			"--conversation-id", "conv_123",
			"--include", "file_search_call.results",
		)
	})
}

func TestConversationsItemsRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"conversations:items", "retrieve",
			"--conversation-id", "conv_123",
			"--item-id", "msg_abc",
			"--include", "file_search_call.results",
		)
	})
}

func TestConversationsItemsList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"conversations:items", "list",
			"--max-items", "10",
			"--conversation-id", "conv_123",
			"--after", "after",
			"--include", "file_search_call.results",
			"--limit", "0",
			"--order", "asc",
		)
	})
}

func TestConversationsItemsDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"conversations:items", "delete",
			"--conversation-id", "conv_123",
			"--item-id", "msg_abc",
		)
	})
}
