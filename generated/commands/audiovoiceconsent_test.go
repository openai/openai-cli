// File generated from our OpenAPI spec by Castiron. See CONTRIBUTING.md for details.

package commands

import (
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestAudioVoiceConsentsCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"audio:voice-consents", "create",
			"--language", "language",
			"--name", "name",
			"--recording", mocktest.TestFile(t, "Example data"),
		)
	})

	t.Run("piping data", func(t *testing.T) {
		testFile := mocktest.TestFile(t, "Example data")
		// Test piping YAML data over stdin
		pipeDataStr := "" +
			"language: language\n" +
			"name: name\n" +
			"recording: Example data\n"
		pipeDataStr = strings.ReplaceAll(pipeDataStr, "Example data", testFile)
		pipeData := []byte(pipeDataStr)
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"audio:voice-consents", "create",
		)
	})
}

func TestAudioVoiceConsentsRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"audio:voice-consents", "retrieve",
			"--consent-id", "consent_id",
		)
	})
}

func TestAudioVoiceConsentsUpdate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"audio:voice-consents", "update",
			"--consent-id", "consent_id",
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
			"audio:voice-consents", "update",
			"--consent-id", "consent_id",
		)
	})
}

func TestAudioVoiceConsentsList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"audio:voice-consents", "list",
			"--max-items", "10",
			"--after", "after",
			"--limit", "0",
		)
	})
}

func TestAudioVoiceConsentsDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"audio:voice-consents", "delete",
			"--consent-id", "consent_id",
		)
	})
}
