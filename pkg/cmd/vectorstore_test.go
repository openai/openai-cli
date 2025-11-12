// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
)

func TestVectorStoresCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores", "create",
			"--chunking-strategy", "{type: auto}",
			"--description", "description",
			"--expires-after", "{anchor: last_active_at, days: 1}",
			"--file-id", "string",
			"--metadata", "{foo: string}",
			"--name", "name",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(vectorStoresCreate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores", "create",
			"--chunking-strategy", "{type: auto}",
			"--description", "description",
			"--expires-after.anchor", "last_active_at",
			"--expires-after.days", "1",
			"--file-id", "string",
			"--metadata", "{foo: string}",
			"--name", "name",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"chunking_strategy:\n" +
			"  type: auto\n" +
			"description: description\n" +
			"expires_after:\n" +
			"  anchor: last_active_at\n" +
			"  days: 1\n" +
			"file_ids:\n" +
			"  - string\n" +
			"metadata:\n" +
			"  foo: string\n" +
			"name: name\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores", "create",
		)
	})
}

func TestVectorStoresRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores", "retrieve",
			"--vector-store-id", "vector_store_id",
		)
	})
}

func TestVectorStoresUpdate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores", "update",
			"--vector-store-id", "vector_store_id",
			"--expires-after", "{anchor: last_active_at, days: 1}",
			"--metadata", "{foo: string}",
			"--name", "name",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(vectorStoresUpdate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores", "update",
			"--vector-store-id", "vector_store_id",
			"--expires-after.anchor", "last_active_at",
			"--expires-after.days", "1",
			"--metadata", "{foo: string}",
			"--name", "name",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"expires_after:\n" +
			"  anchor: last_active_at\n" +
			"  days: 1\n" +
			"metadata:\n" +
			"  foo: string\n" +
			"name: name\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores", "update",
			"--vector-store-id", "vector_store_id",
		)
	})
}

func TestVectorStoresList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores", "list",
			"--max-items", "10",
			"--after", "after",
			"--before", "before",
			"--limit", "0",
			"--order", "asc",
		)
	})
}

func TestVectorStoresDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores", "delete",
			"--vector-store-id", "vector_store_id",
		)
	})
}

func TestVectorStoresSearch(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores", "search",
			"--max-items", "10",
			"--vector-store-id", "vs_abc123",
			"--query", "string",
			"--filters", "{key: key, type: eq, value: string}",
			"--max-num-results", "1",
			"--ranking-options", "{ranker: none, score_threshold: 0}",
			"--rewrite-query=true",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(vectorStoresSearch)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores", "search",
			"--max-items", "10",
			"--vector-store-id", "vs_abc123",
			"--query", "string",
			"--filters", "{key: key, type: eq, value: string}",
			"--max-num-results", "1",
			"--ranking-options.ranker", "none",
			"--ranking-options.score-threshold", "0",
			"--rewrite-query=true",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"query: string\n" +
			"filters:\n" +
			"  key: key\n" +
			"  type: eq\n" +
			"  value: string\n" +
			"max_num_results: 1\n" +
			"ranking_options:\n" +
			"  ranker: none\n" +
			"  score_threshold: 0\n" +
			"rewrite_query: true\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"vector-stores", "search",
			"--max-items", "10",
			"--vector-store-id", "vs_abc123",
		)
	})
}
