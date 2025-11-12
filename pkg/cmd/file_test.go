// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
)

func TestFilesCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"files", "create",
			"--file", mocktest.TestFile(t, "Example data"),
			"--purpose", "assistants",
			"--expires-after", "{anchor: created_at, seconds: 3600}",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(filesCreate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"files", "create",
			"--file", mocktest.TestFile(t, "Example data"),
			"--purpose", "assistants",
			"--expires-after.anchor", "created_at",
			"--expires-after.seconds", "3600",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		testFile := mocktest.TestFile(t, "Example data")
		// Test piping YAML data over stdin
		pipeDataStr := "" +
			"file: Example data\n" +
			"purpose: assistants\n" +
			"expires_after:\n" +
			"  anchor: created_at\n" +
			"  seconds: 3600\n"
		pipeDataStr = strings.ReplaceAll(pipeDataStr, "Example data", testFile)
		pipeData := []byte(pipeDataStr)
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"files", "create",
		)
	})
}

func TestFilesRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"files", "retrieve",
			"--file-id", "file_id",
		)
	})
}

func TestFilesList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"files", "list",
			"--max-items", "10",
			"--after", "after",
			"--limit", "0",
			"--order", "asc",
			"--purpose", "purpose",
		)
	})
}

func TestFilesDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"files", "delete",
			"--file-id", "file_id",
		)
	})
}

func TestFilesContent(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"files", "content",
			"--file-id", "file_id",
			"--output", "/dev/null",
		)
	})
}
