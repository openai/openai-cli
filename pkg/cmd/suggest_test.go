package cmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

// These tests exercise cli.SuggestCommand, the suggester the command tree
// installs through custom.ConfigureCommand, against the real generated commands.

// findCommand resolves a command path in the real command tree.
func findCommand(t *testing.T, path ...string) *cli.Command {
	t.Helper()
	command := Command
	for _, name := range path {
		var next *cli.Command
		for _, sub := range command.Commands {
			if sub.Name == name {
				next = sub
			}
		}
		if next == nil {
			t.Fatalf("command %q not found under %q", name, command.Name)
		}
		command = next
	}
	return command
}

// editDistance is the optimal string alignment distance: each insertion,
// deletion, substitution, or adjacent transposition costs one.
func editDistance(a, b string) int {
	d := make([][]int, len(a)+1)
	for i := range d {
		d[i] = make([]int, len(b)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(a)][len(b)]
}

// Parent links are only set once the command tree runs, so these suggestions
// name the matched command alone; cmd/openai checks the full "openai ..." form.
func TestSuggestCommandRealTree(t *testing.T) {
	tests := []struct {
		path     []string
		provided string
		want     string
	}{
		{[]string{"fine-tuning:alpha:graders"}, "rn", "run"},
		{[]string{"fine-tuning:alpha:graders"}, "rnu", "run"},
		{[]string{"fine-tuning:alpha:graders"}, "urn", "run"},
		{[]string{"fine-tuning:alpha:graders"}, "RN", "run"},
		{[]string{"fine-tuning:alpha:graders"}, "valdate", "validate"},
		{[]string{"fine-tuning:alpha:graders"}, "zzz", ""},
		{[]string{"responses"}, "zzzzz", ""},
		{[]string{"models"}, "ls", "list"},     // jaro-winkler 0.85
		{[]string{"models"}, "lete", "delete"}, // jaro-winkler 0.72
		// Both are one edit away; the higher jaro-winkler score wins.
		{[]string{"admin:organization:certificates"}, "dactivate", "deactivate"},
		// Exact 7/10 jaro scores that float error would otherwise let through.
		{[]string{"fine-tuning:jobs"}, "status", ""},
		{nil, "hi", ""},
		{nil, "RESPONSES", "responses"},
		{nil, "respones", "responses"},
		{nil, "chat:completion", "chat:completions"},
		{nil, "comp", "completions"},
		// Jaro-winkler alone prefers admin:organization:spend-alerts here.
		{nil, "admin:prganization:roles", "admin:organization:roles"},
		{nil, "version", ""}, // jaro-winkler 0.69
		{nil, "totallybogus", ""},
	}
	for _, tc := range tests {
		t.Run(strings.Join(append(tc.path, tc.provided), " "), func(t *testing.T) {
			want := ""
			if tc.want != "" {
				want = fmt.Sprintf("Did you mean '%s'?", tc.want)
			}
			if got := cli.SuggestCommand(findCommand(t, tc.path...).Commands, tc.provided); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

// Every command in the real tree keeps its suggestion for a dropped or swapped
// character or for all-caps input, unless the typo is as close to a sibling.
func TestSuggestCommandRealTreeSingleEdits(t *testing.T) {
	var visit func(parent *cli.Command)
	visit = func(parent *cli.Command) {
		for _, command := range parent.Commands {
			name := command.Name
			typos := []string{strings.ToUpper(name)}
			for i := range name {
				typos = append(typos, name[:i]+name[i+1:])
				if i+1 < len(name) {
					typos = append(typos, name[:i]+name[i+1:i+2]+name[i:i+1]+name[i+2:])
				}
			}
		typos:
			for _, typo := range typos {
				for _, sibling := range parent.Commands {
					if sibling != command && editDistance(sibling.Name, strings.ToLower(typo)) <= 1 {
						continue typos
					}
				}
				want := fmt.Sprintf("Did you mean '%s'?", name)
				if got := cli.SuggestCommand(parent.Commands, typo); got != want {
					t.Errorf("%s: SuggestCommand(%q) = %q, want %q", parent.Name, typo, got, want)
				}
			}
			visit(command)
		}
	}
	visit(Command)
}
