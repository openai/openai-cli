// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestAdminOrganizationProjectsSpendLimitRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:spend-limit", "retrieve",
			"--project-id", "proj_123",
		)
	})
}

func TestAdminOrganizationProjectsSpendLimitUpdate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:spend-limit", "update",
			"--project-id", "proj_123",
			"--currency", "USD",
			"--interval", "month",
			"--threshold-amount", "1",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"currency: USD\n" +
			"interval: month\n" +
			"threshold_amount: 1\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:spend-limit", "update",
			"--project-id", "proj_123",
		)
	})
}

func TestAdminOrganizationProjectsSpendLimitDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:spend-limit", "delete",
			"--project-id", "proj_123",
		)
	})
}
