// File generated from our OpenAPI spec by Castiron. See CONTRIBUTING.md for details.

package commands

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestAdminOrganizationExternalKeysCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:external-keys", "create",
			"--external-id", "external_id",
			"--kms-arn", "kms_arn",
			"--name", "name",
			"--role-arn", "arn:aws:iam::123456789012:role/example-role",
			"--type", "aws",
			"--purpose", "ekm",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"external_id: external_id\n" +
			"kms_arn: kms_arn\n" +
			"name: name\n" +
			"role_arn: arn:aws:iam::123456789012:role/example-role\n" +
			"type: aws\n" +
			"purpose: ekm\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:external-keys", "create",
		)
	})
}

func TestAdminOrganizationExternalKeysRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:external-keys", "retrieve",
			"--external-key-id", "external_key_id",
		)
	})
}

func TestAdminOrganizationExternalKeysList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:external-keys", "list",
			"--max-items", "10",
			"--after", "after",
			"--limit", "0",
			"--order", "asc",
			"--purpose", "ekm",
		)
	})
}

func TestAdminOrganizationExternalKeysDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:external-keys", "delete",
			"--external-key-id", "external_key_id",
		)
	})
}

func TestAdminOrganizationExternalKeysValidate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:external-keys", "validate",
			"--external-key-id", "external_key_id",
		)
	})
}
