// File generated from our OpenAPI spec by Castiron. See CONTRIBUTING.md for details.

package cmd

import (
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestAudioVoicesCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"audio:voices", "create",
			"--audio-sample", mocktest.TestFile(t, "Example data"),
			"--consent", "consent",
			"--name", "x",
			"--type", "audio_sample",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		testFile := mocktest.TestFile(t, "Example data")
		// Test piping YAML data over stdin
		pipeDataStr := "" +
			"audio_sample: Example data\n" +
			"consent: consent\n" +
			"name: x\n" +
			"type: audio_sample\n"
		pipeDataStr = strings.ReplaceAll(pipeDataStr, "Example data", testFile)
		pipeData := []byte(pipeDataStr)
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"audio:voices", "create",
		)
	})
}
