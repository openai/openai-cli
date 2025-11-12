// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestAudioSpeechCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"audio:speech", "create",
			"--input", "input",
			"--model", "tts-1",
			"--voice", "alloy",
			"--instructions", "instructions",
			"--response-format", "mp3",
			"--speed", "0.25",
			"--stream-format", "sse",
			"--output", "/dev/null",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"input: input\n" +
			"model: tts-1\n" +
			"voice: alloy\n" +
			"instructions: instructions\n" +
			"response_format: mp3\n" +
			"speed: 0.25\n" +
			"stream_format: sse\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"audio:speech", "create",
			"--output", "/dev/null",
		)
	})
}
