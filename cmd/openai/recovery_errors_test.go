package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMainRecoveryCommandSuggestions(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"root suffix", []string{"modles", "list"}, "Unknown command. Did you mean: openai models list?"},
		{"nested suffix", []string{"audio", "transcriptons", "create"}, "Unknown command. Did you mean: openai audio transcriptions create?"},
		{"nested operation", []string{"files", "lsit"}, "Unknown command. Did you mean: openai files list?"},
		{"help suffix", []string{"help", "modles", "list"}, "Unknown help topic. Did you mean: openai help models list?"},
		{"help is not a help topic", []string{"help", "hepl"}, "Unknown help topic. Run openai help to see commands."},
		{"nested help", []string{"audio", "help", "transcriptons", "create"}, "Unknown help topic. Did you mean: openai help audio transcriptions create?"},
		{"help flag", []string{"--help", "modles", "list"}, "Unknown help topic. Did you mean: openai help models list?"},
		{"false help", []string{"--help=false", "modles", "list"}, "Unknown command. Did you mean: openai models list?"},
		{"flag boundary", []string{"modles", "list", "--api-key", "synthetic-private-key"}, "Unknown command. Did you mean: openai models list?"},
		{"operand boundary", []string{"modles", "list", "synthetic-private-value", "delete"}, "Unknown command. Did you mean: openai models list?"},
		{"unrecognized suffix", []string{"modles", "synthetic-private-value", "list"}, "Unknown command. Did you mean: openai models?"},
		{"help-looking credential", []string{"--api-key=--help", "modles", "list"}, "Unknown command. Did you mean: openai models list?"},
		{"private command", []string{"https://user:synthetic-private-key@example.invalid/?token=secret\x1b[31m"}, "Unknown command. Run openai help to see commands."},
	} {
		for _, format := range []string{"text", "json", "jsonl"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				args := append([]string{"openai", "--format-error", format}, tc.args...)
				got := runMainDispatch(t, "bash", args...)
				if got.code != 3 || got.stdout != "" {
					t.Fatalf("result = %+v; want status 3 and stderr only", got)
				}
				message := got.stderr
				if format != "text" {
					payload := decodeMainStructuredError(t, format, message)
					message, _ = payload["message"].(string)
				}
				if strings.TrimSpace(message) != tc.want {
					t.Errorf("message = %q; want %q", message, tc.want)
				}
				if strings.Contains(got.stderr, "synthetic-private-") || strings.ContainsRune(got.stderr, '\x1b') {
					t.Errorf("unsafe diagnostic = %q", got.stderr)
				}
			})
		}
	}
}

func TestMainRecoveryFileOptionsAcrossFamilies(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	missing := filepath.Join(t.TempDir(), "synthetic-private-key\x1b[31m.txt")
	for _, tc := range []struct {
		name   string
		args   []string
		stdin  string
		option string
	}{
		{name: "positional upload", args: []string{"files", "upload", missing, "--purpose", "user_data"}, option: "--file"},
		{name: "generated upload", args: []string{"files", "create", "--file", missing, "--purpose", "user_data"}, option: "--file"},
		{name: "audio upload", args: []string{"audio", "transcribe", "--file", missing, "--model", "synthetic-model"}, option: "--file"},
		{name: "upload part", args: []string{"uploads", "parts", "create", "--upload-id", "synthetic-upload", "--data", missing}, option: "--data"},
		{name: "JSON file", args: []string{"responses", "create", "--model", "synthetic-model", "--input", "@" + missing}, option: "--input"},
		{name: "query file", args: []string{"files", "list", "--after", "@" + missing}, option: "--after"},
		{name: "piped file", args: []string{"files", "upload"}, stdin: recoveryJSON(t, map[string]any{"file": missing, "purpose": "user_data"}), option: "--file"},
		{name: "nested field", args: []string{"chat", "completions", "create", "--model", "synthetic-model", "--message", recoveryJSON(t, map[string]any{"role": "user", "content": "@" + missing})}, option: "--message"},
	} {
		for _, format := range []string{"text", "json", "jsonl", "yaml"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				var stdin *os.File
				if tc.stdin != "" {
					path := filepath.Join(t.TempDir(), "request.json")
					if err := os.WriteFile(path, []byte(tc.stdin), 0600); err != nil {
						t.Fatal(err)
					}
					var err error
					stdin, err = os.Open(path)
					if err != nil {
						t.Fatal(err)
					}
					defer stdin.Close()
				}
				args := append([]string{"openai", "--base-url", server.URL, "--format-error", format}, tc.args...)
				got := runMainDispatchWithStdin(t, "bash", nil, stdin, args...)
				if got.code != 1 || got.stdout != "" {
					t.Fatalf("result = %+v; want status 1 and stderr only", got)
				}
				message := got.stderr
				if format != "text" {
					payload := decodeMainStructuredError(t, format, message)
					message, _ = payload["message"].(string)
				}
				want := "Could not open the file for " + tc.option + ". Check the path and permissions."
				if strings.TrimSpace(message) != want {
					t.Errorf("message = %q; want %q", message, want)
				}
				if strings.Contains(got.stderr, "synthetic-private-") || strings.Contains(got.stderr, filepath.Dir(missing)) || strings.ContainsRune(got.stderr, '\x1b') {
					t.Errorf("unsafe diagnostic = %q", got.stderr)
				}
			})
		}
	}
	if requests.Load() != 0 {
		t.Errorf("local file failures sent %d requests", requests.Load())
	}
}

func recoveryJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
