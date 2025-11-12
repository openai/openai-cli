// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
)

func TestRealtimeClientSecretsCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"realtime:client-secrets", "create",
			"--expires-after", "{anchor: created_at, seconds: 10}",
			"--session", "{type: realtime, audio: {input: {format: {rate: 24000, type: audio/pcm}, noise_reduction: {type: near_field}, transcription: {language: language, model: whisper-1, prompt: prompt}, turn_detection: {type: server_vad, create_response: true, idle_timeout_ms: 5000, interrupt_response: true, prefix_padding_ms: 0, silence_duration_ms: 0, threshold: 0}}, output: {format: {rate: 24000, type: audio/pcm}, speed: 0.25, voice: alloy}}, include: [item.input_audio_transcription.logprobs], instructions: instructions, max_output_tokens: inf, model: gpt-realtime, output_modalities: [text], prompt: {id: id, variables: {foo: string}, version: version}, tool_choice: none, tools: [{description: description, name: name, parameters: {}, type: function}], tracing: auto, truncation: auto}",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(realtimeClientSecretsCreate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"realtime:client-secrets", "create",
			"--expires-after.anchor", "created_at",
			"--expires-after.seconds", "10",
			"--session", "{type: realtime, audio: {input: {format: {rate: 24000, type: audio/pcm}, noise_reduction: {type: near_field}, transcription: {language: language, model: whisper-1, prompt: prompt}, turn_detection: {type: server_vad, create_response: true, idle_timeout_ms: 5000, interrupt_response: true, prefix_padding_ms: 0, silence_duration_ms: 0, threshold: 0}}, output: {format: {rate: 24000, type: audio/pcm}, speed: 0.25, voice: alloy}}, include: [item.input_audio_transcription.logprobs], instructions: instructions, max_output_tokens: inf, model: gpt-realtime, output_modalities: [text], prompt: {id: id, variables: {foo: string}, version: version}, tool_choice: none, tools: [{description: description, name: name, parameters: {}, type: function}], tracing: auto, truncation: auto}",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"expires_after:\n" +
			"  anchor: created_at\n" +
			"  seconds: 10\n" +
			"session:\n" +
			"  type: realtime\n" +
			"  audio:\n" +
			"    input:\n" +
			"      format:\n" +
			"        rate: 24000\n" +
			"        type: audio/pcm\n" +
			"      noise_reduction:\n" +
			"        type: near_field\n" +
			"      transcription:\n" +
			"        language: language\n" +
			"        model: whisper-1\n" +
			"        prompt: prompt\n" +
			"      turn_detection:\n" +
			"        type: server_vad\n" +
			"        create_response: true\n" +
			"        idle_timeout_ms: 5000\n" +
			"        interrupt_response: true\n" +
			"        prefix_padding_ms: 0\n" +
			"        silence_duration_ms: 0\n" +
			"        threshold: 0\n" +
			"    output:\n" +
			"      format:\n" +
			"        rate: 24000\n" +
			"        type: audio/pcm\n" +
			"      speed: 0.25\n" +
			"      voice: alloy\n" +
			"  include:\n" +
			"    - item.input_audio_transcription.logprobs\n" +
			"  instructions: instructions\n" +
			"  max_output_tokens: inf\n" +
			"  model: gpt-realtime\n" +
			"  output_modalities:\n" +
			"    - text\n" +
			"  prompt:\n" +
			"    id: id\n" +
			"    variables:\n" +
			"      foo: string\n" +
			"    version: version\n" +
			"  tool_choice: none\n" +
			"  tools:\n" +
			"    - description: description\n" +
			"      name: name\n" +
			"      parameters: {}\n" +
			"      type: function\n" +
			"  tracing: auto\n" +
			"  truncation: auto\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"realtime:client-secrets", "create",
		)
	})
}
