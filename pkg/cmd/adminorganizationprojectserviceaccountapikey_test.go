// File generated from our OpenAPI spec by Castiron. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestAdminOrganizationProjectsServiceAccountsAPIKeysCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:service-accounts:api-keys", "create",
			"--project-id", "project_id",
			"--service-account-id", "service_account_id",
			"--expires-in-seconds", "1",
			"--name", "name",
			"--scope", "string",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"expires_in_seconds: 1\n" +
			"name: name\n" +
			"scopes:\n" +
			"  - string\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:service-accounts:api-keys", "create",
			"--project-id", "project_id",
			"--service-account-id", "service_account_id",
		)
	})
}
