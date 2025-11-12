// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
)

func TestVectorStoresFileBatchesCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores:file-batches", "create",
			"--vector-store-id", "vs_abc123",
			"--attributes", "{foo: string}",
			"--chunking-strategy", "{type: auto}",
			"--file-id", "string",
			"--file", "{file_id: file_id, attributes: {foo: string}, chunking_strategy: {type: auto}}",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(vectorStoresFileBatchesCreate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores:file-batches", "create",
			"--vector-store-id", "vs_abc123",
			"--attributes", "{foo: string}",
			"--chunking-strategy", "{type: auto}",
			"--file-id", "string",
			"--file.file-id", "file_id",
			"--file.attributes", "{foo: string}",
			"--file.chunking-strategy", "{type: auto}",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"attributes:\n" +
			"  foo: string\n" +
			"chunking_strategy:\n" +
			"  type: auto\n" +
			"file_ids:\n" +
			"  - string\n" +
			"files:\n" +
			"  - file_id: file_id\n" +
			"    attributes:\n" +
			"      foo: string\n" +
			"    chunking_strategy:\n" +
			"      type: auto\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores:file-batches", "create",
			"--vector-store-id", "vs_abc123",
		)
	})
}

func TestVectorStoresFileBatchesRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores:file-batches", "retrieve",
			"--vector-store-id", "vs_abc123",
			"--batch-id", "vsfb_abc123",
		)
	})
}

func TestVectorStoresFileBatchesCancel(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores:file-batches", "cancel",
			"--vector-store-id", "vector_store_id",
			"--batch-id", "batch_id",
		)
	})
}

func TestVectorStoresFileBatchesListFiles(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores:file-batches", "list-files",
			"--max-items", "10",
			"--vector-store-id", "vector_store_id",
			"--batch-id", "batch_id",
			"--after", "after",
			"--before", "before",
			"--filter", "in_progress",
			"--limit", "0",
			"--order", "asc",
		)
	})
}
