// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

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
