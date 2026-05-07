// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
)

func TestRealtimeCallsAccept(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"realtime:calls", "accept",
			"--call-id", "call_id",
			"--type", "realtime",
			"--audio", "{input: {format: {rate: 24000, type: audio/pcm}, noise_reduction: {type: near_field}, transcription: {delay: minimal, language: language, model: whisper-1, prompt: prompt}, turn_detection: {type: server_vad, create_response: true, idle_timeout_ms: 5000, interrupt_response: true, prefix_padding_ms: 0, silence_duration_ms: 0, threshold: 0}}, output: {format: {rate: 24000, type: audio/pcm}, speed: 0.25, voice: alloy}}",
			"--include", "item.input_audio_transcription.logprobs",
			"--instructions", "instructions",
			"--max-output-tokens", "inf",
			"--model", "gpt-realtime",
			"--output-modality", "text",
			"--parallel-tool-calls=true",
			"--prompt", "{id: id, variables: {foo: string}, version: version}",
			"--reasoning", "{effort: minimal}",
			"--tool-choice", "none",
			"--tool", "{description: description, name: name, parameters: {}, type: function}",
			"--tracing", "auto",
			"--truncation", "auto",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(realtimeCallsAccept)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"realtime:calls", "accept",
			"--call-id", "call_id",
			"--type", "realtime",
			"--audio.input", "{format: {rate: 24000, type: audio/pcm}, noise_reduction: {type: near_field}, transcription: {delay: minimal, language: language, model: whisper-1, prompt: prompt}, turn_detection: {type: server_vad, create_response: true, idle_timeout_ms: 5000, interrupt_response: true, prefix_padding_ms: 0, silence_duration_ms: 0, threshold: 0}}",
			"--audio.output", "{format: {rate: 24000, type: audio/pcm}, speed: 0.25, voice: alloy}",
			"--include", "item.input_audio_transcription.logprobs",
			"--instructions", "instructions",
			"--max-output-tokens", "inf",
			"--model", "gpt-realtime",
			"--output-modality", "text",
			"--parallel-tool-calls=true",
			"--prompt.id", "id",
			"--prompt.variables", "{foo: string}",
			"--prompt.version", "version",
			"--reasoning.effort", "minimal",
			"--tool-choice", "none",
			"--tool", "{description: description, name: name, parameters: {}, type: function}",
			"--tracing", "auto",
			"--truncation", "auto",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"type: realtime\n" +
			"audio:\n" +
			"  input:\n" +
			"    format:\n" +
			"      rate: 24000\n" +
			"      type: audio/pcm\n" +
			"    noise_reduction:\n" +
			"      type: near_field\n" +
			"    transcription:\n" +
			"      delay: minimal\n" +
			"      language: language\n" +
			"      model: whisper-1\n" +
			"      prompt: prompt\n" +
			"    turn_detection:\n" +
			"      type: server_vad\n" +
			"      create_response: true\n" +
			"      idle_timeout_ms: 5000\n" +
			"      interrupt_response: true\n" +
			"      prefix_padding_ms: 0\n" +
			"      silence_duration_ms: 0\n" +
			"      threshold: 0\n" +
			"  output:\n" +
			"    format:\n" +
			"      rate: 24000\n" +
			"      type: audio/pcm\n" +
			"    speed: 0.25\n" +
			"    voice: alloy\n" +
			"include:\n" +
			"  - item.input_audio_transcription.logprobs\n" +
			"instructions: instructions\n" +
			"max_output_tokens: inf\n" +
			"model: gpt-realtime\n" +
			"output_modalities:\n" +
			"  - text\n" +
			"parallel_tool_calls: true\n" +
			"prompt:\n" +
			"  id: id\n" +
			"  variables:\n" +
			"    foo: string\n" +
			"  version: version\n" +
			"reasoning:\n" +
			"  effort: minimal\n" +
			"tool_choice: none\n" +
			"tools:\n" +
			"  - description: description\n" +
			"    name: name\n" +
			"    parameters: {}\n" +
			"    type: function\n" +
			"tracing: auto\n" +
			"truncation: auto\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"realtime:calls", "accept",
			"--call-id", "call_id",
		)
	})
}

func TestRealtimeCallsHangup(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"realtime:calls", "hangup",
			"--call-id", "call_id",
		)
	})
}

func TestRealtimeCallsRefer(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"realtime:calls", "refer",
			"--call-id", "call_id",
			"--target-uri", "tel:+14155550123",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("target_uri: tel:+14155550123")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"realtime:calls", "refer",
			"--call-id", "call_id",
		)
	})
}

func TestRealtimeCallsReject(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"realtime:calls", "reject",
			"--call-id", "call_id",
			"--status-code", "486",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("status_code: 486")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"realtime:calls", "reject",
			"--call-id", "call_id",
		)
	})
}
