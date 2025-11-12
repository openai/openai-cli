// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestBetaChatKitThreadsRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:chatkit:threads", "retrieve",
			"--thread-id", "cthr_123",
		)
	})
}

func TestBetaChatKitThreadsList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:chatkit:threads", "list",
			"--max-items", "10",
			"--after", "after",
			"--before", "before",
			"--limit", "0",
			"--order", "asc",
			"--user", "x",
		)
	})
}

func TestBetaChatKitThreadsDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:chatkit:threads", "delete",
			"--thread-id", "cthr_123",
		)
	})
}

func TestBetaChatKitThreadsListItems(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:chatkit:threads", "list-items",
			"--max-items", "10",
			"--thread-id", "cthr_123",
			"--after", "after",
			"--before", "before",
			"--limit", "0",
			"--order", "asc",
		)
	})
}
