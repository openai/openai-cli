// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestEmbeddingsCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"embeddings", "create",
			"--input", "The quick brown fox jumped over the lazy dog",
			"--model", "text-embedding-3-small",
			"--dimensions", "1",
			"--encoding-format", "float",
			"--user", "user-1234",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"input: The quick brown fox jumped over the lazy dog\n" +
			"model: text-embedding-3-small\n" +
			"dimensions: 1\n" +
			"encoding_format: float\n" +
			"user: user-1234\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"embeddings", "create",
		)
	})
}
