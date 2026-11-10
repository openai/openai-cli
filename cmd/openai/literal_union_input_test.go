package main

import "testing"

func TestMainRealtimeMaxOutputTokensPreservesNumber(t *testing.T) {
	server, requests := globalFlagsServer(t, `{}`)
	got := runShellFileCommand(t, server, nil, nil, "realtime", "calls", "accept", "--call-id", "call_test", "--max-output-tokens", "1024")
	if got.code != 0 || got.stderr != "" {
		t.Fatalf("command: %+v", got)
	}
	inputPolicyValue(t, inputPolicyBody(t, requests), "max_output_tokens", float64(1024))
}

func TestMainUnionNestedInputPreservesTypedCollections(t *testing.T) {
	server, requests := globalFlagsServer(t, `{"id":"run_test","object":"thread.run","status":"queued"}`)
	for _, tc := range []struct {
		name    string
		args    []string
		success bool
		want    any
	}{
		{"array then nested", []string{"--additional-message", "[]", "--additional-message.role", "user"}, true, []any{map[string]any{"role": "user"}}},
		{"nested then parent object", []string{"--additional-message.role", "user", "--additional-message", `{"role":"assistant"}`}, true, []any{map[string]any{"role": "user"}, map[string]any{"role": "assistant"}}},
		{"unavailable companion before nested", []string{"--additional-message-structured", "[]", "--additional-message.role", "user"}, false, nil},
		{"unavailable companion after nested", []string{"--additional-message.role", "user", "--additional-message-structured", "[]"}, false, nil},
	} {
		for _, mode := range []string{"literal", "legacy"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				base := []string{"beta", "threads", "runs", "create", "--thread-id", "thread_test", "--assistant-id", "asst_test"}
				got := runShellFileCommand(t, server, nil, []string{"OPENAI_CLI_INPUT_MODE=" + mode}, append(base, tc.args...)...)
				if !tc.success {
					if got.code == 0 {
						t.Fatalf("unavailable flag succeeded: %+v", got)
					}
					inputPolicyNoRequest(t, requests)
					return
				}
				if got.code != 0 || got.stderr != "" {
					t.Fatalf("command: %+v", got)
				}
				inputPolicyValue(t, inputPolicyBody(t, requests), "additional_messages", tc.want)
			})
		}
	}
}

func TestMainUnionPipedInputAndExplicitOverride(t *testing.T) {
	server, requests := inputPolicyServer(t)
	for _, tc := range []struct {
		name, data string
		flags      []string
		want       any
	}{
		{"json array", `{"input":[{"role":"user","content":"hello"}]}`, nil, []any{map[string]any{"role": "user", "content": "hello"}}},
		{"yaml array", "input:\n  - role: user\n    content: hello\n", nil, []any{map[string]any{"role": "user", "content": "hello"}}},
		{"literal override", `{"input":["piped"]}`, []string{"--input", "null"}, "null"},
		{"structured override", `{"input":"piped"}`, []string{"--input-structured", `["explicit"]`}, []any{"explicit"}},
	} {
		for _, untrusted := range []string{"0", "1"} {
			t.Run(tc.name+"/untrusted="+untrusted, func(t *testing.T) {
				stdin := shellFileInput(t, []byte(tc.data))
				args := append([]string{"responses", "create", "--model", "test-model"}, tc.flags...)
				got := runShellFileCommand(t, server, stdin, []string{"OPENAI_UNTRUSTED_STDIN=" + untrusted}, args...)
				if got.code != 0 || got.stderr != "" {
					t.Fatalf("command: %+v", got)
				}
				inputPolicyValue(t, inputPolicyBody(t, requests), "input", tc.want)
			})
		}
	}
}
