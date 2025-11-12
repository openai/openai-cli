// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
)

func TestBatchesCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"batches", "create",
			"--completion-window", "24h",
			"--endpoint", "/v1/responses",
			"--input-file-id", "input_file_id",
			"--metadata", "{foo: string}",
			"--output-expires-after", "{anchor: created_at, seconds: 3600}",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(batchesCreate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"batches", "create",
			"--completion-window", "24h",
			"--endpoint", "/v1/responses",
			"--input-file-id", "input_file_id",
			"--metadata", "{foo: string}",
			"--output-expires-after.anchor", "created_at",
			"--output-expires-after.seconds", "3600",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"completion_window: 24h\n" +
			"endpoint: /v1/responses\n" +
			"input_file_id: input_file_id\n" +
			"metadata:\n" +
			"  foo: string\n" +
			"output_expires_after:\n" +
			"  anchor: created_at\n" +
			"  seconds: 3600\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"batches", "create",
		)
	})
}

func TestBatchesRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"batches", "retrieve",
			"--batch-id", "batch_id",
		)
	})
}

func TestBatchesList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"batches", "list",
			"--max-items", "10",
			"--after", "after",
			"--limit", "0",
		)
	})
}

func TestBatchesCancel(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"batches", "cancel",
			"--batch-id", "batch_id",
		)
	})
}
