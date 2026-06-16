// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
)

func TestAdminOrganizationSpendAlertsCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:spend-alerts", "create",
			"--currency", "USD",
			"--interval", "month",
			"--notification-channel", "{recipients: [string], type: email, subject_prefix: subject_prefix}",
			"--threshold-amount", "0",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(adminOrganizationSpendAlertsCreate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:spend-alerts", "create",
			"--currency", "USD",
			"--interval", "month",
			"--notification-channel.recipients", "[string]",
			"--notification-channel.type", "email",
			"--notification-channel.subject-prefix", "subject_prefix",
			"--threshold-amount", "0",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"currency: USD\n" +
			"interval: month\n" +
			"notification_channel:\n" +
			"  recipients:\n" +
			"    - string\n" +
			"  type: email\n" +
			"  subject_prefix: subject_prefix\n" +
			"threshold_amount: 0\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:spend-alerts", "create",
		)
	})
}

func TestAdminOrganizationSpendAlertsRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:spend-alerts", "retrieve",
			"--alert-id", "alert_id",
		)
	})
}

func TestAdminOrganizationSpendAlertsUpdate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:spend-alerts", "update",
			"--alert-id", "alert_id",
			"--currency", "USD",
			"--interval", "month",
			"--notification-channel", "{recipients: [string], type: email, subject_prefix: subject_prefix}",
			"--threshold-amount", "0",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(adminOrganizationSpendAlertsUpdate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:spend-alerts", "update",
			"--alert-id", "alert_id",
			"--currency", "USD",
			"--interval", "month",
			"--notification-channel.recipients", "[string]",
			"--notification-channel.type", "email",
			"--notification-channel.subject-prefix", "subject_prefix",
			"--threshold-amount", "0",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"currency: USD\n" +
			"interval: month\n" +
			"notification_channel:\n" +
			"  recipients:\n" +
			"    - string\n" +
			"  type: email\n" +
			"  subject_prefix: subject_prefix\n" +
			"threshold_amount: 0\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:spend-alerts", "update",
			"--alert-id", "alert_id",
		)
	})
}

func TestAdminOrganizationSpendAlertsList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:spend-alerts", "list",
			"--max-items", "10",
			"--after", "after",
			"--before", "before",
			"--limit", "0",
			"--order", "asc",
		)
	})
}

func TestAdminOrganizationSpendAlertsDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:spend-alerts", "delete",
			"--alert-id", "alert_id",
		)
	})
}
