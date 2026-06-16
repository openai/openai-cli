// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
)

func TestAdminOrganizationAuditLogsList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:audit-logs", "list",
			"--max-items", "10",
			"--actor-email", "string",
			"--actor-id", "string",
			"--after", "after",
			"--before", "before",
			"--effective-at", "{gt: 0, gte: 0, lt: 0, lte: 0}",
			"--event-type", "api_key.created",
			"--limit", "0",
			"--project-id", "string",
			"--resource-id", "string",
			"--tenant-only=true",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(adminOrganizationAuditLogsList)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:audit-logs", "list",
			"--max-items", "10",
			"--actor-email", "string",
			"--actor-id", "string",
			"--after", "after",
			"--before", "before",
			"--effective-at.gt", "0",
			"--effective-at.gte", "0",
			"--effective-at.lt", "0",
			"--effective-at.lte", "0",
			"--event-type", "api_key.created",
			"--limit", "0",
			"--project-id", "string",
			"--resource-id", "string",
			"--tenant-only=true",
		)
	})
}
