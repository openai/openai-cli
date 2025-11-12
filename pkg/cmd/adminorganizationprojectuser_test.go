// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestAdminOrganizationProjectsUsersCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:users", "create",
			"--project-id", "project_id",
			"--role", "owner",
			"--user-id", "user_id",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"role: owner\n" +
			"user_id: user_id\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:users", "create",
			"--project-id", "project_id",
		)
	})
}

func TestAdminOrganizationProjectsUsersRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:users", "retrieve",
			"--project-id", "project_id",
			"--user-id", "user_id",
		)
	})
}

func TestAdminOrganizationProjectsUsersUpdate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:users", "update",
			"--project-id", "project_id",
			"--user-id", "user_id",
			"--role", "owner",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("role: owner")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:users", "update",
			"--project-id", "project_id",
			"--user-id", "user_id",
		)
	})
}

func TestAdminOrganizationProjectsUsersList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:users", "list",
			"--max-items", "10",
			"--project-id", "project_id",
			"--after", "after",
			"--limit", "0",
		)
	})
}

func TestAdminOrganizationProjectsUsersDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:users", "delete",
			"--project-id", "project_id",
			"--user-id", "user_id",
		)
	})
}
