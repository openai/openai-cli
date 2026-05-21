// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestAdminOrganizationProjectsGroupsRolesCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:groups:roles", "create",
			"--project-id", "project_id",
			"--group-id", "group_id",
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
			"admin:organization:projects:groups:roles", "create",
			"--project-id", "project_id",
			"--group-id", "group_id",
		)
	})
}

func TestAdminOrganizationProjectsGroupsRolesRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:groups:roles", "retrieve",
			"--project-id", "project_id",
			"--group-id", "group_id",
			"--role-id", "role_id",
		)
	})
}

func TestAdminOrganizationProjectsGroupsRolesList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:groups:roles", "list",
			"--max-items", "10",
			"--project-id", "project_id",
			"--group-id", "group_id",
			"--after", "after",
			"--limit", "0",
			"--order", "asc",
		)
	})
}

func TestAdminOrganizationProjectsGroupsRolesDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:groups:roles", "delete",
			"--project-id", "project_id",
			"--group-id", "group_id",
			"--role-id", "role_id",
		)
	})
}
