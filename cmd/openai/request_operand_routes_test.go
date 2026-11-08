package main

import "testing"

func TestMainRequestOperandRoutes(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want map[string]any
	}{
		{
			"chat stop",
			[]string{"chat", "completions", "create", "--model", "model_demo", "--message", `{"role":"user","content":"hello"}`, "--stop", "###"},
			map[string]any{"stop": "###"},
		},
		{
			"completion prompt and stop",
			[]string{"completions", "create", "--model", "model_demo", "--prompt", "line one\nline two", "--stop", "Human:"},
			map[string]any{"prompt": "line one\nline two", "stop": "Human:"},
		},
		{
			"completion stop hash",
			[]string{"completions", "create", "--model", "model_demo", "--prompt", "hello", "--stop", "###"},
			map[string]any{"stop": "###"},
		},
		{
			"vector store search",
			[]string{"vector-stores", "search", "--vector-store-id", "vs_demo", "--query", "error: timeout"},
			map[string]any{"query": "error: timeout"},
		},
		{
			"response input and conversation",
			[]string{"responses", "create", "--model", "model_demo", "--input", "Translate: hello", "--conversation", "conv_demo", "--tool-choice", "{type: function, name: example}"},
			map[string]any{"input": "Translate: hello", "conversation": "conv_demo", "tool_choice": map[string]any{"type": "function", "name": "example"}},
		},
		{
			"response structured conversation",
			[]string{"responses", "create", "--model", "model_demo", "--input", "#1 priority", "--conversation-structured", `{"id":"conv_demo"}`},
			map[string]any{"input": "#1 priority", "conversation": map[string]any{"id": "conv_demo"}},
		},
		{
			"embedding text",
			[]string{"embeddings", "create", "--model", "model_demo", "--input", "[123,456]"},
			map[string]any{"input": "[123,456]"},
		},
		{
			"embedding token IDs",
			[]string{"embeddings", "create", "--model", "model_demo", "--input-structured", "[123,456]"},
			map[string]any{"input": []any{float64(123), float64(456)}},
		},
		{
			"moderation input",
			[]string{"moderations", "create", "--input", "Translate: hello"},
			map[string]any{"input": "Translate: hello"},
		},
		{
			"decision input",
			[]string{"decisions", "create", "--model", "model_demo", "--question", `{"id":"demo","description":"Is this a greeting?"}`, "--input", "Translate: hello"},
			map[string]any{"input": "Translate: hello"},
		},
		{
			"chat prediction and audio",
			[]string{"chat", "completions", "create", "--model", "model_demo", "--message", `{"role":"user","content":"hello"}`, "--prediction.content", "Translate: hello", "--audio.voice", "voice: demo"},
			map[string]any{"prediction": map[string]any{"content": "Translate: hello"}, "audio": map[string]any{"voice": "voice: demo"}},
		},
		{
			"thread message content",
			[]string{"beta", "threads", "messages", "create", "--thread-id", "thread_demo", "--role", "user", "--content", "Translate: hello"},
			map[string]any{"content": "Translate: hello"},
		},
		{
			"initial thread message",
			[]string{"beta", "threads", "create", "--message.content", "Translate: hello"},
			map[string]any{"messages": []any{map[string]any{"content": "Translate: hello"}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, requests := globalFlagsServer(t, `{"id":"synthetic","object":"list","data":[]}`)
			got := runShellFileCommand(t, server, nil, nil, tc.args...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("command exited %d: %s", got.code, got.stderr)
			}
			body := inputPolicyBody(t, requests)
			for key, want := range tc.want {
				inputPolicyValue(t, body, key, want)
			}
		})
	}
}
