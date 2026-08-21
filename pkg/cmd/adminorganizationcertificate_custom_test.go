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

func TestAdminOrganizationCertificatesCreatePEM(t *testing.T) {
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
