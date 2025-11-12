// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
)

func TestBetaChatKitSessionsCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:chatkit:sessions", "create",
			"--user", "x",
			"--workflow", "{id: id, state_variables: {foo: string}, tracing: {enabled: true}, version: version}",
			"--chatkit-configuration", "{automatic_thread_titling: {enabled: true}, file_upload: {enabled: true, max_file_size: 1, max_files: 1}, history: {enabled: true, recent_threads: 1}}",
			"--expires-after", "{anchor: created_at, seconds: 1}",
			"--rate-limits", "{max_requests_per_1_minute: 1}",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(betaChatKitSessionsCreate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:chatkit:sessions", "create",
			"--user", "x",
			"--workflow.id", "id",
			"--workflow.state-variables", "{foo: string}",
			"--workflow.tracing", "{enabled: true}",
			"--workflow.version", "version",
			"--chatkit-configuration.automatic-thread-titling", "{enabled: true}",
			"--chatkit-configuration.file-upload", "{enabled: true, max_file_size: 1, max_files: 1}",
			"--chatkit-configuration.history", "{enabled: true, recent_threads: 1}",
			"--expires-after.anchor", "created_at",
			"--expires-after.seconds", "1",
			"--rate-limits.max-requests-per-1-minute", "1",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"user: x\n" +
			"workflow:\n" +
			"  id: id\n" +
			"  state_variables:\n" +
			"    foo: string\n" +
			"  tracing:\n" +
			"    enabled: true\n" +
			"  version: version\n" +
			"chatkit_configuration:\n" +
			"  automatic_thread_titling:\n" +
			"    enabled: true\n" +
			"  file_upload:\n" +
			"    enabled: true\n" +
			"    max_file_size: 1\n" +
			"    max_files: 1\n" +
			"  history:\n" +
			"    enabled: true\n" +
			"    recent_threads: 1\n" +
			"expires_after:\n" +
			"  anchor: created_at\n" +
			"  seconds: 1\n" +
			"rate_limits:\n" +
			"  max_requests_per_1_minute: 1\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:chatkit:sessions", "create",
		)
	})
}

func TestBetaChatKitSessionsCancel(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"beta:chatkit:sessions", "cancel",
			"--session-id", "cksess_123",
		)
	})
}
