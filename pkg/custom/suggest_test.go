package custom

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
			name:     "uppercase input is matched ignoring case",
			provided: "CREAT",
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

func TestWithinOneEdit(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"", "", true},
		{"", "a", true},
		{"", "ab", false},
		{"run", "run", true},
		{"run", "rn", true},   // deletion
		{"run", "runn", true}, // insertion
		{"run", "ran", true},  // substitution
		{"run", "rnu", true},  // transposition
		{"run", "urn", true},  // transposition
		{"ab", "ba", true},    // transposition
		{"run", "rm", false},
		{"run", "nur", false},
		{"list", "ls", false},
		{"create", "craete", true},
		{"create", "carete", false},
		{"create", "caerte", false},
		{"run", "urm", false}, // swapped pair plus another change
		{"run", "xrn", false}, // only one side of the swap matches
	}
	for _, tc := range tests {
		for _, pair := range [][2]string{{tc.a, tc.b}, {tc.b, tc.a}} {
			if got := withinOneEdit(pair[0], pair[1]); got != tc.want {
				t.Errorf("withinOneEdit(%q, %q) = %v, want %v", pair[0], pair[1], got, tc.want)
			}
		}
	}
}
