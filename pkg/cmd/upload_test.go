// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
)

func TestUploadsCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"uploads", "create",
			"--bytes", "0",
			"--filename", "filename",
			"--mime-type", "mime_type",
			"--purpose", "assistants",
			"--expires-after", "{anchor: created_at, seconds: 3600}",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(uploadsCreate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"uploads", "create",
			"--bytes", "0",
			"--filename", "filename",
			"--mime-type", "mime_type",
			"--purpose", "assistants",
			"--expires-after.anchor", "created_at",
			"--expires-after.seconds", "3600",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"bytes: 0\n" +
			"filename: filename\n" +
			"mime_type: mime_type\n" +
			"purpose: assistants\n" +
			"expires_after:\n" +
			"  anchor: created_at\n" +
			"  seconds: 3600\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"uploads", "create",
		)
	})
}

func TestUploadsCancel(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"uploads", "cancel",
			"--upload-id", "upload_abc123",
		)
	})
}

func TestUploadsComplete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"uploads", "complete",
			"--upload-id", "upload_abc123",
			"--part-id", "string",
			"--md5", "md5",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"part_ids:\n" +
			"  - string\n" +
			"md5: md5\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"uploads", "complete",
			"--upload-id", "upload_abc123",
		)
	})
}
