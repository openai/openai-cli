// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
)

func TestAdminOrganizationProjectsHostedToolPermissionsRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:hosted-tool-permissions", "retrieve",
			"--project-id", "project_id",
		)
	})
}

func TestAdminOrganizationProjectsHostedToolPermissionsUpdate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:hosted-tool-permissions", "update",
			"--project-id", "project_id",
			"--code-interpreter", "{enabled: true}",
			"--file-search", "{enabled: true}",
			"--image-generation", "{enabled: true}",
			"--mcp", "{enabled: true}",
			"--web-search", "{enabled: true}",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(adminOrganizationProjectsHostedToolPermissionsUpdate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:hosted-tool-permissions", "update",
			"--project-id", "project_id",
			"--code-interpreter.enabled=true",
			"--file-search.enabled=true",
			"--image-generation.enabled=true",
			"--mcp.enabled=true",
			"--web-search.enabled=true",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"code_interpreter:\n" +
			"  enabled: true\n" +
			"file_search:\n" +
			"  enabled: true\n" +
			"image_generation:\n" +
			"  enabled: true\n" +
			"mcp:\n" +
			"  enabled: true\n" +
			"web_search:\n" +
			"  enabled: true\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:hosted-tool-permissions", "update",
			"--project-id", "project_id",
		)
	})
}
