// File generated from our OpenAPI spec by Castiron. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
)

func TestLiveSessionsAccept(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"live:sessions", "accept",
			"--session-id", "session_id",
			"--session", "{model: gpt-live-1, type: live, audio: {output: {voice: alloy}}, delegation: {type: client}, input: [{content: [{text: text, type: input_text}], role: developer, id: id, status: incomplete, type: message}], instructions: instructions, store: true}",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(liveSessionsAccept)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"live:sessions", "accept",
			"--session-id", "session_id",
			"--session.model", "gpt-live-1",
			"--session.type", "live",
			"--session.audio", "{output: {voice: alloy}}",
			"--session.delegation", "{type: client}",
			"--session.input", "[{content: [{text: text, type: input_text}], role: developer, id: id, status: incomplete, type: message}]",
			"--session.instructions", "instructions",
			"--session.store=true",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"session:\n" +
			"  model: gpt-live-1\n" +
			"  type: live\n" +
			"  audio:\n" +
			"    output:\n" +
			"      voice: alloy\n" +
			"  delegation:\n" +
			"    type: client\n" +
			"  input:\n" +
			"    - content:\n" +
			"        - text: text\n" +
			"          type: input_text\n" +
			"      role: developer\n" +
			"      id: id\n" +
			"      status: incomplete\n" +
			"      type: message\n" +
			"  instructions: instructions\n" +
			"  store: true\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"live:sessions", "accept",
			"--session-id", "session_id",
		)
	})
}

func TestLiveSessionsDownloadRecording(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"live:sessions", "download-recording",
			"--session-id", "live_SQ",
			"--output", "/dev/null",
		)
	})
}

func TestLiveSessionsFork(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"live:sessions", "fork",
			"--session-id", "session_id",
			"--transport", "{sdp: x, type: webrtc}",
			"--session", "{client: {data_channel: {allowed_client_events: all, allowed_server_events: all}}, delegation: {type: responses, responses: {instructions: instructions, max_output_tokens: 16, model: model, parallel_tool_calls: true, reasoning: {effort: none, summary: concise}, service_tier: auto, text: {verbosity: low}, tool_choice: auto, tools: [{name: name, type: function, description: description, parameters: {foo: bar}, strict: true}]}}, store: true}",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(liveSessionsFork)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"live:sessions", "fork",
			"--session-id", "session_id",
			"--transport.sdp", "x",
			"--transport.type", "webrtc",
			"--session.client", "{data_channel: {allowed_client_events: all, allowed_server_events: all}}",
			"--session.delegation", "{type: responses, responses: {instructions: instructions, max_output_tokens: 16, model: model, parallel_tool_calls: true, reasoning: {effort: none, summary: concise}, service_tier: auto, text: {verbosity: low}, tool_choice: auto, tools: [{name: name, type: function, description: description, parameters: {foo: bar}, strict: true}]}}",
			"--session.store=true",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"transport:\n" +
			"  sdp: x\n" +
			"  type: webrtc\n" +
			"session:\n" +
			"  client:\n" +
			"    data_channel:\n" +
			"      allowed_client_events: all\n" +
			"      allowed_server_events: all\n" +
			"  delegation:\n" +
			"    type: responses\n" +
			"    responses:\n" +
			"      instructions: instructions\n" +
			"      max_output_tokens: 16\n" +
			"      model: model\n" +
			"      parallel_tool_calls: true\n" +
			"      reasoning:\n" +
			"        effort: none\n" +
			"        summary: concise\n" +
			"      service_tier: auto\n" +
			"      text:\n" +
			"        verbosity: low\n" +
			"      tool_choice: auto\n" +
			"      tools:\n" +
			"        - name: name\n" +
			"          type: function\n" +
			"          description: description\n" +
			"          parameters:\n" +
			"            foo: bar\n" +
			"          strict: true\n" +
			"  store: true\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"live:sessions", "fork",
			"--session-id", "session_id",
		)
	})
}

func TestLiveSessionsHangup(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"live:sessions", "hangup",
			"--session-id", "session_id",
		)
	})
}

func TestLiveSessionsRefer(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"live:sessions", "refer",
			"--session-id", "session_id",
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
			"live:sessions", "refer",
			"--session-id", "session_id",
		)
	})
}

func TestLiveSessionsReject(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"live:sessions", "reject",
			"--session-id", "session_id",
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
			"live:sessions", "reject",
			"--session-id", "session_id",
		)
	})
}
