// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestAdminOrganizationGroupsRolesCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:groups:roles", "create",
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
			"admin:organization:groups:roles", "create",
			"--group-id", "group_id",
		)
	})
}

func TestAdminOrganizationGroupsRolesList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:groups:roles", "list",
			"--max-items", "10",
			"--group-id", "group_id",
			"--after", "after",
			"--limit", "0",
			"--order", "asc",
		)
	})
}

func TestAdminOrganizationGroupsRolesDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:groups:roles", "delete",
			"--group-id", "group_id",
			"--role-id", "role_id",
		)
	})
}
