// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestImagesCreateVariation(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"images", "create-variation",
			"--image", mocktest.TestFile(t, "Example data"),
			"--model", "gpt-image-1",
			"--n", "1",
			"--response-format", "url",
			"--size", "1024x1024",
			"--user", "user-1234",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		testFile := mocktest.TestFile(t, "Example data")
		// Test piping YAML data over stdin
		pipeDataStr := "" +
			"image: Example data\n" +
			"model: gpt-image-1\n" +
			"'n': 1\n" +
			"response_format: url\n" +
			"size: 1024x1024\n" +
			"user: user-1234\n"
		pipeDataStr = strings.ReplaceAll(pipeDataStr, "Example data", testFile)
		pipeData := []byte(pipeDataStr)
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"images", "create-variation",
		)
	})
}

func TestImagesEdit(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"images", "edit",
			"--max-items", "10",
			"--image", mocktest.TestFile(t, "Example data"),
			"--prompt", "A cute baby sea otter wearing a beret",
			"--background", "transparent",
			"--input-fidelity", "high",
			"--mask", mocktest.TestFile(t, "Example data"),
			"--model", "gpt-image-2",
			"--n", "1",
			"--output-compression", "100",
			"--output-format", "png",
			"--partial-images", "1",
			"--quality", "high",
			"--response-format", "url",
			"--size", "256x256",
			"--stream=false",
			"--user", "user-1234",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		testFile := mocktest.TestFile(t, "Example data")
		// Test piping YAML data over stdin
		pipeDataStr := "" +
			"image: Example data\n" +
			"prompt: A cute baby sea otter wearing a beret\n" +
			"background: transparent\n" +
			"input_fidelity: high\n" +
			"mask: Example data\n" +
			"model: gpt-image-2\n" +
			"'n': 1\n" +
			"output_compression: 100\n" +
			"output_format: png\n" +
			"partial_images: 1\n" +
			"quality: high\n" +
			"response_format: url\n" +
			"size: 256x256\n" +
			"stream: false\n" +
			"user: user-1234\n"
		pipeDataStr = strings.ReplaceAll(pipeDataStr, "Example data", testFile)
		pipeData := []byte(pipeDataStr)
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"images", "edit",
			"--max-items", "10",
		)
	})
}

func TestImagesGenerate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"images", "generate",
			"--max-items", "10",
			"--prompt", "A cute baby sea otter",
			"--background", "transparent",
			"--model", "gpt-image-2",
			"--moderation", "low",
			"--n", "1",
			"--output-compression", "100",
			"--output-format", "png",
			"--partial-images", "1",
			"--quality", "medium",
			"--response-format", "url",
			"--size", "auto",
			"--stream=false",
			"--style", "vivid",
			"--user", "user-1234",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"prompt: A cute baby sea otter\n" +
			"background: transparent\n" +
			"model: gpt-image-2\n" +
			"moderation: low\n" +
			"'n': 1\n" +
			"output_compression: 100\n" +
			"output_format: png\n" +
			"partial_images: 1\n" +
			"quality: medium\n" +
			"response_format: url\n" +
			"size: auto\n" +
			"stream: false\n" +
			"style: vivid\n" +
			"user: user-1234\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"images", "generate",
			"--max-items", "10",
		)
	})
}
