// File generated from our OpenAPI spec by Castiron. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestWebhooksCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"webhooks", "create",
			"--event-type", "batch.completed",
			"--name", "x",
			"--url", "https://",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"event_types:\n" +
			"  - batch.completed\n" +
			"name: x\n" +
			"url: https://\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"webhooks", "create",
		)
	})
}

func TestWebhooksRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"webhooks", "retrieve",
			"--webhook-endpoint-id", "whe_123",
		)
	})
}

func TestWebhooksUpdate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"webhooks", "update",
			"--webhook-endpoint-id", "whe_123",
			"--event-type", "batch.completed",
			"--name", "x",
			"--url", "https://",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"event_types:\n" +
			"  - batch.completed\n" +
			"name: x\n" +
			"url: https://\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"webhooks", "update",
			"--webhook-endpoint-id", "whe_123",
		)
	})
}

func TestWebhooksList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"webhooks", "list",
			"--max-items", "10",
			"--after", "whe_123",
			"--limit", "1",
		)
	})
}

func TestWebhooksDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"webhooks", "delete",
			"--webhook-endpoint-id", "whe_123",
		)
	})
}

func TestWebhooksRotateSecret(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"webhooks", "rotate-secret",
			"--webhook-endpoint-id", "whe_123",
			"--keep-old-secret-active-for-24-hours=true",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("keep_old_secret_active_for_24_hours: true")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"webhooks", "rotate-secret",
			"--webhook-endpoint-id", "whe_123",
		)
	})
}

func TestWebhooksTest(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"webhooks", "test",
			"--webhook-endpoint-id", "whe_123",
			"--event-type", "batch.completed",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("event_type: batch.completed")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"webhooks", "test",
			"--webhook-endpoint-id", "whe_123",
		)
	})
}
