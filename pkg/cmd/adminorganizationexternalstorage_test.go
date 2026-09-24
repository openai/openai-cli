// File generated from our OpenAPI spec by Castiron. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestAdminOrganizationExternalStorageCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:external-storage", "create",
			"--project-id", "proj_123",
			"--provider", "{bucket: bucket, role_arn: role_arn, type: aws}",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"project_id: proj_123\n" +
			"provider:\n" +
			"  bucket: bucket\n" +
			"  role_arn: role_arn\n" +
			"  type: aws\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:external-storage", "create",
		)
	})
}

func TestAdminOrganizationExternalStorageRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:external-storage", "retrieve",
			"--external-storage-id", "extstorage_123",
		)
	})
}

func TestAdminOrganizationExternalStorageList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:external-storage", "list",
			"--max-items", "10",
			"--after", "after",
			"--limit", "1",
			"--order", "asc",
			"--project-id", "proj_123",
		)
	})
}

func TestAdminOrganizationExternalStorageDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:external-storage", "delete",
			"--external-storage-id", "extstorage_123",
		)
	})
}

func TestAdminOrganizationExternalStorageValidate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:external-storage", "validate",
			"--external-storage-id", "extstorage_123",
		)
	})
}
