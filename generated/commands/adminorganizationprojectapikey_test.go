// File generated from our OpenAPI spec by Castiron. See CONTRIBUTING.md for details.

package commands

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestAdminOrganizationProjectsAPIKeysRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:api-keys", "retrieve",
			"--project-id", "project_id",
			"--api-key-id", "api_key_id",
		)
	})
}

func TestAdminOrganizationProjectsAPIKeysUpdate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:api-keys", "update",
			"--project-id", "project_id",
			"--api-key-id", "api_key_id",
			"--expires-in-seconds", "1",
			"--scope", "api.all.read",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"expires_in_seconds: 1\n" +
			"scopes:\n" +
			"  - api.all.read\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:api-keys", "update",
			"--project-id", "project_id",
			"--api-key-id", "api_key_id",
		)
	})
}

func TestAdminOrganizationProjectsAPIKeysList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:api-keys", "list",
			"--max-items", "10",
			"--project-id", "project_id",
			"--after", "after",
			"--limit", "0",
			"--owner-project-access", "active",
		)
	})
}

func TestAdminOrganizationProjectsAPIKeysDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:api-keys", "delete",
			"--project-id", "project_id",
			"--api-key-id", "api_key_id",
		)
	})
}
