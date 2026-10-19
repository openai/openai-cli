package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMainNestedFlagErrorRecovery(t *testing.T) {
	for _, route := range [][]string{{"chat", "completions"}, {"chat:completions"}} {
		for _, tc := range []struct{ name, operation, flag, message string }{
			{"invalid boolean", "create", "--stream-options.include-usage=synthetic-private-value", "Invalid value for --stream-options.include-usage. Expected a boolean (true or false)."},
			{"nested typo", "create", "--stream-options.include-usgae=synthetic-private-value", "An option is not recognized. Did you mean --stream-options.include-usage?"},
			{"nested option elsewhere", "list", "--stream-options.include-usage=synthetic-private-value", "The --stream-options.include-usage option is not available for this command."},
		} {
			for _, format := range []string{"text", "json"} {
				t.Run(strings.Join(route, "/")+"/"+tc.name+"/"+format, func(t *testing.T) {
					var requests atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						requests.Add(1)
						w.WriteHeader(http.StatusBadRequest)
					}))
					defer server.Close()
					args := append([]string{"--format-error", format}, route...)
					args = append(args, tc.operation)
					if tc.operation == "create" {
						args = append(args, "--model", "synthetic-private-model", "--message", `{"role":"user","content":"synthetic-private-prompt"}`)
					}
					args = append(args, tc.flag)
					got := runShellFileCommand(t, server, nil, []string{"NO_COLOR=1"}, args...)
					if got.code != 1 || got.stdout != "" || requests.Load() != 0 {
						t.Fatalf("nested parser failure changed status, streams, or requests: %+v; requests=%d", got, requests.Load())
					}
					if strings.Contains(got.stderr, "synthetic-private") || strings.ContainsAny(got.stderr, "\x1b\a\r") {
						t.Fatalf("nested diagnostic exposed private arguments or controls: %q", got.stderr)
					}
					message := strings.TrimSpace(got.stderr)
					if format == "json" {
						payload := decodeMainStructuredError(t, format, got.stderr)
						if len(payload) != 1 {
							t.Fatalf("local parser error invented fields: %#v", payload)
						}
						message, _ = payload["message"].(string)
					}
					help := "openai " + strings.Join(route, " ") + " " + tc.operation + " --help"
					want := tc.message + "\nOptions and examples: " + help
					if message != want {
						t.Fatalf("got %q, want %q", message, want)
					}
					_, copiedHelp, found := strings.Cut(message, "Options and examples: ")
					if !found {
						t.Fatal("missing scoped recovery help")
					}
					recovered := runShellFileCommand(t, server, nil, nil, strings.Fields(copiedHelp)[1:]...)
					if recovered.code != 0 || recovered.stderr != "" || !strings.Contains(recovered.stdout, "openai "+strings.Join(route, " ")+" "+tc.operation) || requests.Load() != 0 {
						t.Fatalf("copied nested recovery help failed: %+v; requests=%d", recovered, requests.Load())
					}
				})
			}
		}
	}
}
