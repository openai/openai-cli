package cmd

import (
	"testing"

	"github.com/urfave/cli/v3"
)

func TestSuggestCommand(t *testing.T) {
	commands := []*cli.Command{
		{Name: "create"},
		{Name: "retrieve"},
		{Name: "list"},
		{Name: "delete"},
		{Name: "chat:completions"},
		{Name: "completions"},
	}

	tests := []struct {
		name     string
		provided string
		want     string
	}{
		{
			name:     "close typo suggests the corrected command",
			provided: "creat",
			want:     "Did you mean 'create'?",
		},
		{
			name:     "near-exact suggests the corrected command",
			provided: "chat:completion",
			want:     "Did you mean 'chat:completions'?",
		},
		{
			name:     "exact match still suggests itself",
			provided: "create",
			want:     "Did you mean 'create'?",
		},
		{
			name:     "unrelated input returns no suggestion",
			provided: "zzzzz",
			want:     "",
		},
		{
			name:     "low-similarity input returns no suggestion",
			provided: "totallybogus",
			want:     "",
		},
		{
			name:     "empty input returns no suggestion",
			provided: "",
			want:     "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := suggestCommand(commands, tc.provided)
			if got != tc.want {
				t.Errorf("suggestCommand(%q) = %q, want %q", tc.provided, got, tc.want)
			}
		})
	}
}

func TestSuggestCommandEmptyCommands(t *testing.T) {
	if got := suggestCommand(nil, "anything"); got != "" {
		t.Errorf("suggestCommand(nil, %q) = %q, want empty string", "anything", got)
	}
}
