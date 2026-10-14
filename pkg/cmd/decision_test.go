// File generated from our OpenAPI spec by Castiron. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestDecisionsCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"decisions", "create",
			"--input", "string",
			"--model", "model",
			"--question", "{instructions: instructions, type: predicate, name: name}",
			"--safety-identifier", "safety_identifier",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"input: string\n" +
			"model: model\n" +
			"questions:\n" +
			"  - instructions: instructions\n" +
			"    type: predicate\n" +
			"    name: name\n" +
			"safety_identifier: safety_identifier\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"decisions", "create",
		)
	})
}
