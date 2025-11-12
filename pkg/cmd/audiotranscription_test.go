// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestAudioTranscriptionsCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"audio:transcriptions", "create",
			"--max-items", "10",
			"--file", mocktest.TestFile(t, "Example data"),
			"--model", "gpt-4o-transcribe",
			"--chunking-strategy", "auto",
			"--include", "logprobs",
			"--known-speaker-name", "string",
			"--known-speaker-reference", "string",
			"--language", "language",
			"--prompt", "prompt",
			"--response-format", "json",
			"--stream=false",
			"--temperature", "0",
			"--timestamp-granularity", "word",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		testFile := mocktest.TestFile(t, "Example data")
		// Test piping YAML data over stdin
		pipeDataStr := "" +
			"file: Example data\n" +
			"model: gpt-4o-transcribe\n" +
			"chunking_strategy: auto\n" +
			"include:\n" +
			"  - logprobs\n" +
			"known_speaker_names:\n" +
			"  - string\n" +
			"known_speaker_references:\n" +
			"  - string\n" +
			"language: language\n" +
			"prompt: prompt\n" +
			"response_format: json\n" +
			"stream: false\n" +
			"temperature: 0\n" +
			"timestamp_granularities:\n" +
			"  - word\n"
		pipeDataStr = strings.ReplaceAll(pipeDataStr, "Example data", testFile)
		pipeData := []byte(pipeDataStr)
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"audio:transcriptions", "create",
			"--max-items", "10",
		)
	})
}
