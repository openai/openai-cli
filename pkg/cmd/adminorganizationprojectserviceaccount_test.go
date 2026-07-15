// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestAdminOrganizationProjectsServiceAccountsCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:service-accounts", "create",
			"--project-id", "project_id",
			"--name", "name",
			"--create-service-account-only=true",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"name: name\n" +
			"create_service_account_only: true\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:service-accounts", "create",
			"--project-id", "project_id",
		)
	})
}

func TestAdminOrganizationProjectsServiceAccountsRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:service-accounts", "retrieve",
			"--project-id", "project_id",
			"--service-account-id", "service_account_id",
		)
	})
}

func TestAdminOrganizationProjectsServiceAccountsUpdate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:service-accounts", "update",
			"--project-id", "project_id",
			"--service-account-id", "service_account_id",
			"--name", "name",
			"--role", "member",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"name: name\n" +
			"role: member\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:service-accounts", "update",
			"--project-id", "project_id",
			"--service-account-id", "service_account_id",
		)
	})
}

func TestAdminOrganizationProjectsServiceAccountsList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:service-accounts", "list",
			"--max-items", "10",
			"--project-id", "project_id",
			"--after", "after",
			"--limit", "0",
		)
	})
}

func TestAdminOrganizationProjectsServiceAccountsDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:service-accounts", "delete",
			"--project-id", "project_id",
			"--service-account-id", "service_account_id",
		)
	})
}
