// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestAdminOrganizationUsersRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:users", "retrieve",
			"--user-id", "user_id",
		)
	})
}

func TestAdminOrganizationUsersUpdate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:users", "update",
			"--user-id", "user_id",
			"--developer-persona", "developer_persona",
			"--role", "role",
			"--role-id", "role_id",
			"--technical-level", "technical_level",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"developer_persona: developer_persona\n" +
			"role: role\n" +
			"role_id: role_id\n" +
			"technical_level: technical_level\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:users", "update",
			"--user-id", "user_id",
		)
	})
}

func TestAdminOrganizationUsersList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:users", "list",
			"--max-items", "10",
			"--after", "after",
			"--email", "string",
			"--limit", "0",
		)
	})
}

func TestAdminOrganizationUsersDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:users", "delete",
			"--user-id", "user_id",
		)
	})
}
