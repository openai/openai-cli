// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestAdminOrganizationProjectsUsersRolesCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:users:roles", "create",
			"--project-id", "project_id",
			"--user-id", "user_id",
			"--role-id", "role_id",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("role_id: role_id")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:users:roles", "create",
			"--project-id", "project_id",
			"--user-id", "user_id",
		)
	})
}

func TestAdminOrganizationProjectsUsersRolesList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:users:roles", "list",
			"--max-items", "10",
			"--project-id", "project_id",
			"--user-id", "user_id",
			"--after", "after",
			"--limit", "0",
			"--order", "asc",
		)
	})
}

func TestAdminOrganizationProjectsUsersRolesDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:users:roles", "delete",
			"--project-id", "project_id",
			"--user-id", "user_id",
			"--role-id", "role_id",
		)
	})
}
