package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMainInputPolicyNestedWholeArray(t *testing.T) {
	server, requests := globalFlagsServer(t, `{
		"id":"alert_synthetic","object":"organization.spend_alert",
		"threshold_amount":100,"currency":"USD","interval":"month",
		"notification_channel":{"type":"email","recipients":["synthetic@example.test","second@example.test"]}
	}`)
	for _, mode := range []string{"literal", "legacy"} {
		for _, route := range [][]string{
			{"admin", "organization", "spend-alerts", "create"},
			{"admin:organization:spend-alerts", "create"},
		} {
			t.Run(mode+"/"+route[0], func(t *testing.T) {
				args := append(append([]string{}, route...), "--currency", "USD", "--interval", "month", "--threshold-amount", "100", "--notification-channel.type", "email", "--notification-channel.recipients", `["synthetic@example.test","second@example.test"]`)
				got := runShellFileCommand(t, server, nil, []string{"OPENAI_CLI_INPUT_MODE=" + mode, "OPENAI_ADMIN_KEY=sk-fake-input-policy-admin"}, args...)
				if got.code != 0 || got.stderr != "" {
					t.Fatalf("command: %+v", got)
				}
				inputPolicyValue(t, inputPolicyBody(t, requests), "notification_channel", map[string]any{
					"type": "email", "recipients": []any{"synthetic@example.test", "second@example.test"},
				})
			})
		}
	}
}

func TestMainInputPolicyTypedParentAfterNested(t *testing.T) {
	server, requests := inputPolicyServer(t)
	for _, mode := range []string{"literal", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			got := runShellFileCommand(t, server, nil, []string{"OPENAI_CLI_INPUT_MODE=" + mode},
				"responses", "create", "--model", "model_synthetic", "--input", "synthetic",
				"--context-management.type", "compaction", "--context-management.compact-threshold", "1024",
				"--context-management", `{"type":"compaction","compact_threshold":2048}`)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("command: %+v", got)
			}
			inputPolicyValue(t, inputPolicyBody(t, requests), "context_management", []any{
				map[string]any{"type": "compaction", "compact_threshold": float64(1024)},
				map[string]any{"type": "compaction", "compact_threshold": float64(2048)},
			})
		})
	}
}

func TestMainInputPolicyEmbeddedFileCompatibility(t *testing.T) {
	server, requests := inputPolicyServer(t)
	path := filepath.Join(t.TempDir(), "synthetic.txt")
	const content = "synthetic: file # contents\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	reference := "@" + path
	input := []any{map[string]any{"role": "user", "content": reference}}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	bodyJSON, err := json.Marshal(map[string]any{"model": "model_synthetic", "input": input})
	if err != nil {
		t.Fatal(err)
	}
	quotedReference, err := json.Marshal(reference)
	if err != nil {
		t.Fatal(err)
	}
	bodyYAML := "model: model_synthetic\ninput:\n  - role: user\n    content: " + string(quotedReference) + "\n"
	for _, tc := range []struct {
		name, mode, untrusted, stdin string
		flags                        []string
		want                         any
	}{
		{"legacy flag expansion", "legacy", "true", "", []string{"--model", "model_synthetic", "--input", string(inputJSON)}, []any{map[string]any{"role": "user", "content": content}}},
		{"trusted JSON expansion", "literal", "false", string(bodyJSON), nil, []any{map[string]any{"role": "user", "content": content}}},
		{"trusted YAML expansion", "literal", "false", bodyYAML, nil, []any{map[string]any{"role": "user", "content": content}}},
		{"untrusted JSON preserves reference", "literal", "true", string(bodyJSON), nil, input},
		{"untrusted YAML preserves reference", "literal", "true", bodyYAML, nil, input},
		{"structured protects nested string", "literal", "false", "", []string{"--model", "model_synthetic", "--input-structured", string(inputJSON)}, input},
		{"structured overrides legacy expansion", "legacy", "false", "", []string{"--model", "model_synthetic", "--input-structured", string(inputJSON)}, input},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdin *os.File
			if tc.stdin != "" {
				stdin = shellFileInput(t, []byte(tc.stdin))
			}
			args := append([]string{"responses", "create"}, tc.flags...)
			got := runShellFileCommand(t, server, stdin, []string{"OPENAI_CLI_INPUT_MODE=" + tc.mode, "OPENAI_UNTRUSTED_STDIN=" + tc.untrusted}, args...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("command: %+v", got)
			}
			inputPolicyValue(t, inputPolicyBody(t, requests), "input", tc.want)
		})
	}
}
