// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestVectorStoresFilesCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores:files", "create",
			"--vector-store-id", "vs_abc123",
			"--file-id", "file_id",
			"--attributes", "{foo: string}",
			"--chunking-strategy", "{type: auto}",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"file_id: file_id\n" +
			"attributes:\n" +
			"  foo: string\n" +
			"chunking_strategy:\n" +
			"  type: auto\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores:files", "create",
			"--vector-store-id", "vs_abc123",
		)
	})
}

func TestVectorStoresFilesRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores:files", "retrieve",
			"--vector-store-id", "vs_abc123",
			"--file-id", "file-abc123",
		)
	})
}

func TestVectorStoresFilesUpdate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores:files", "update",
			"--vector-store-id", "vs_abc123",
			"--file-id", "file-abc123",
			"--attributes", "{foo: string}",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"attributes:\n" +
			"  foo: string\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores:files", "update",
			"--vector-store-id", "vs_abc123",
			"--file-id", "file-abc123",
		)
	})
}

func TestVectorStoresFilesList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores:files", "list",
			"--max-items", "10",
			"--vector-store-id", "vector_store_id",
			"--after", "after",
			"--before", "before",
			"--filter", "in_progress",
			"--limit", "0",
			"--order", "asc",
		)
	})
}

func TestVectorStoresFilesDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores:files", "delete",
			"--vector-store-id", "vector_store_id",
			"--file-id", "file_id",
		)
	})
}

func TestVectorStoresFilesContent(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores:files", "content",
			"--max-items", "10",
			"--vector-store-id", "vs_abc123",
			"--file-id", "file-abc123",
		)
	})
}
