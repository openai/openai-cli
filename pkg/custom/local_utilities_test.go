package custom

import (
	"context"
	"io"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestLocalUtilityParsedSelection(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"tokenizer", []string{"tokenizer", "count"}, true},
		{"codex", []string{"codex"}, true},
		{"root value before local", []string{"--project", "responses", "tokenizer", "count"}, true},
		{"root value after local", []string{"tokenizer", "--project", "responses", "count"}, true},
		{"root literal tokenizer", []string{"--project", "tokenizer", "responses"}, false},
		{"root literal codex", []string{"--project", "codex", "responses"}, false},
		{"endpoint literal", []string{"responses", "--input", "tokenizer"}, false},
		{"equals literal", []string{"responses", "--input=codex"}, false},
		{"nested API name", []string{"responses", "codex"}, false},
		{"help helper", []string{"help", "tokenizer"}, false},
		{"completion helper", []string{"__complete", "codex"}, false},
		{"no command", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := false
			action := func(_ context.Context, c *cli.Command) error {
				seen = true
				if got := IsLocalUtilityCommand(c); got != tc.want {
					t.Errorf("action classifier = %v, want %v", got, tc.want)
				}
				return nil
			}
			root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
				HideHelpCommand: true, Action: action,
				Flags: []cli.Flag{&cli.StringFlag{Name: "project"}},
				Commands: []*cli.Command{
					{Name: "tokenizer", Metadata: map[string]any{localUtilityMetadata: true}, Commands: []*cli.Command{{Name: "count", Action: action}}},
					{Name: "codex", Metadata: map[string]any{localUtilityMetadata: true}, Action: action},
					{Name: "responses", Flags: []cli.Flag{&cli.StringFlag{Name: "input"}}, Action: action, Commands: []*cli.Command{{Name: "codex", Action: action}}},
					{Name: "help", Action: action},
					{Name: "__complete", Action: action},
				},
				Before: func(ctx context.Context, c *cli.Command) (context.Context, error) {
					if got := IsLocalUtilityCommand(c); got != tc.want {
						t.Errorf("root classifier = %v, want %v", got, tc.want)
					}
					return ctx, nil
				},
			}
			if err := root.Run(t.Context(), append([]string{"openai"}, tc.args...)); err != nil {
				t.Fatal(err)
			}
			if !seen {
				t.Fatal("command action did not execute")
			}
		})
	}
}
