package main

import (
	"strings"
	"testing"
)

// Unknown commands suggest a locally declared path without echoing the rejected
// argument. Unrelated input gets help guidance without a misleading suggestion.
func TestMainUnknownCommandSuggestions(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		stderr string
	}{
		{[]string{"files", "gt"}, "Unknown command. Did you mean: openai help files get?\nTo run it, correct only the command spelling. Keep your original options and arguments.\n"},
		{[]string{"files", "gte"}, "Unknown command. Did you mean: openai help files get?\nTo run it, correct only the command spelling. Keep your original options and arguments.\n"},
		{[]string{"responses", "creat"}, "Unknown command. Did you mean: openai help responses create?\nTo run it, correct only the command spelling. Keep your original options and arguments.\n"},
		{[]string{"RESPONSES"}, "Unknown command. Did you mean: openai help responses?\nTo run it, correct only the command spelling. Keep your original options and arguments.\n"},
		{[]string{"totallybogus"}, "Unknown command. Run openai help to see commands.\n"},
		{[]string{"responses", "zzzzz"}, "Unknown command. Run openai help responses to see commands.\n"},
	} {
		for _, flags := range [][]string{nil, {"--format-error", "json"}, {"--format", "json"}} {
			t.Run(strings.Join(append(append([]string{}, tc.args...), flags...), "/"), func(t *testing.T) {
				want := mainDispatchResult{3, "", tc.stderr}
				args := append(append([]string{"openai"}, flags...), tc.args...)
				got := runMainDispatch(t, "bash", args...)
				if len(flags) > 0 {
					payload := decodeMainStructuredError(t, "json", got.stderr)
					got.stderr = payload["message"].(string) + "\n"
				}
				if got != want {
					t.Fatalf("got %+v; want %+v", got, want)
				}
			})
		}
	}
}

func TestMainUnknownCommandSuggestionsDoNotEchoInput(t *testing.T) {
	for _, args := range [][]string{
		{"responses", "creat\x1b]52;c;synthetic-private-secret\a"},
		{"synthetic-private-topic'. Did you mean 'synthetic-private-secret'?"},
	} {
		for _, flags := range [][]string{nil, {"--format-error", "json"}} {
			command := append(append([]string{"openai"}, flags...), args...)
			got := runMainDispatch(t, "bash", command...)
			if got.code != 3 || got.stdout != "" || got.stderr == "" {
				t.Fatalf("expected exit 3 with a stderr diagnostic, got %+v", got)
			}
			if strings.Contains(got.stderr, "synthetic-private-") || strings.ContainsAny(got.stderr, "\x1b\a") {
				t.Fatalf("unknown-command diagnostic echoed rejected input: %q", got.stderr)
			}
			if len(flags) > 0 {
				decodeMainStructuredError(t, "json", got.stderr)
			}
		}
	}
}
