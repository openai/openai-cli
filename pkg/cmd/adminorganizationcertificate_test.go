// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

const testCertificatePEM = `-----BEGIN CERTIFICATE-----
MOCK-CERTIFICATE-CONTENT-FOR-OPENAI-CLI-TESTS
THIS-IS-NOT-A-REAL-CERTIFICATE
-----END CERTIFICATE-----`

func TestAdminOrganizationCertificatesCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:certificates", "create",
			"--certificate", testCertificatePEM,
			"--content", testCertificatePEM,
			"--name", "name",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"certificate: |\n" +
			"  " + strings.ReplaceAll(testCertificatePEM, "\n", "\n  ") + "\n" +
			"content: |\n" +
			"  " + strings.ReplaceAll(testCertificatePEM, "\n", "\n  ") + "\n" +
			"name: name\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:certificates", "create",
		)
	})
}

func TestAdminOrganizationCertificatesRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:certificates", "retrieve",
			"--certificate-id", "certificate_id",
			"--include", "content",
		)
	})
}

func TestAdminOrganizationCertificatesUpdate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:certificates", "update",
			"--certificate-id", "certificate_id",
			"--name", "name",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("name: name")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:certificates", "update",
			"--certificate-id", "certificate_id",
		)
	})
}

func TestAdminOrganizationCertificatesList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:certificates", "list",
			"--max-items", "10",
			"--after", "after",
			"--limit", "0",
			"--order", "asc",
		)
	})
}

func TestAdminOrganizationCertificatesDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:certificates", "delete",
			"--certificate-id", "certificate_id",
		)
	})
}

func TestAdminOrganizationCertificatesActivate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:certificates", "activate",
			"--max-items", "10",
			"--certificate-id", "cert_abc",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"certificate_ids:\n" +
			"  - cert_abc\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:certificates", "activate",
			"--max-items", "10",
		)
	})
}

func TestAdminOrganizationCertificatesDeactivate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:certificates", "deactivate",
			"--max-items", "10",
			"--certificate-id", "cert_abc",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"certificate_ids:\n" +
			"  - cert_abc\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:certificates", "deactivate",
			"--max-items", "10",
		)
	})
}
