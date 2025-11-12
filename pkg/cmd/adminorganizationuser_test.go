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
