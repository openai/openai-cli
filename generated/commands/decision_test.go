// File generated from our OpenAPI spec by Castiron. See CONTRIBUTING.md for details.

package commands

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
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
			"--prompt-cache-key", "prompt_cache_key",
			"--prompt-cache-options", "{mode: implicit, ttl: 30m}",
			"--safety-identifier", "safety_identifier",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(decisionsCreate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"decisions", "create",
			"--input", "string",
			"--model", "model",
			"--question", "{instructions: instructions, type: predicate, name: name}",
			"--prompt-cache-key", "prompt_cache_key",
			"--prompt-cache-options.mode", "implicit",
			"--prompt-cache-options.ttl", "30m",
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
			"prompt_cache_key: prompt_cache_key\n" +
			"prompt_cache_options:\n" +
			"  mode: implicit\n" +
			"  ttl: 30m\n" +
			"safety_identifier: safety_identifier\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"decisions", "create",
		)
	})
}
