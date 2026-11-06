package main

import (
	"strings"
	"testing"
)

func TestMainInputPolicyHelpClassifiesOperands(t *testing.T) {
	server, requests := inputPolicyServer(t)
	env := []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY="}
	for _, tc := range []struct {
		path            []string
		present, absent []string
	}{
		{
			path:    []string{"responses", "create"},
			present: []string{"--input-structured", "--instructions-structured", "--previous-response-id-structured"},
			absent:  []string{"--max-output-tokens-structured", "--tool-choice-structured", "--metadata-structured"},
		},
		{
			path:    []string{"beta", "threads", "runs", "create"},
			present: []string{"--additional-message.content-structured", "--instructions-structured"},
			absent:  []string{"--additional-message-structured", "--additional-message.attachments-structured", "--additional-message.role-structured"},
		},
	} {
		t.Run(strings.Join(tc.path, "/"), func(t *testing.T) {
			args := append([]string{"openai", "help"}, tc.path...)
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("help: %+v", got)
			}
			for _, flag := range tc.present {
				if !strings.Contains(got.stdout, flag) {
					t.Errorf("help omits supported flag %s", flag)
				}
			}
			for _, flag := range tc.absent {
				if strings.Contains(got.stdout, flag) {
					t.Errorf("help offers unsupported flag %s", flag)
				}
			}
			inputPolicyNoRequest(t, requests)
		})
	}
}

func TestMainInputPolicyCompletionClassifiesOperands(t *testing.T) {
	server, requests := inputPolicyServer(t)
	env := []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY="}
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		for _, tc := range []struct {
			prefix, want string
		}{
			{"--input-str", "--input-structured"},
			{"--instructions-str", "--instructions-structured"},
			{"--previous-response-id-str", "--previous-response-id-structured"},
			{"--max-output-tokens-str", ""},
			{"--tool-choice-str", ""},
		} {
			t.Run(style+"/"+tc.prefix, func(t *testing.T) {
				got := runMainDispatchWithEnv(t, style, env, mainCompletionArgs(style, "responses", "create", tc.prefix)...)
				if got.code != 0 || got.stderr != "" {
					t.Fatalf("completion: %+v", got)
				}
				if tc.want == "" && got.stdout != "" {
					t.Fatalf("completion offers unsupported companion: %q", got.stdout)
				}
				if tc.want != "" && !strings.Contains(got.stdout, tc.want) {
					t.Fatalf("completion omits %s: %q", tc.want, got.stdout)
				}
				inputPolicyNoRequest(t, requests)
			})
		}
	}
}
