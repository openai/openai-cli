package custom

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func recoveryCommandFixture(t *testing.T) *cli.Command {
	t.Helper()
	leaf := func(name string, aliases ...string) *cli.Command {
		return &cli.Command{Name: name, Aliases: aliases, HideHelpCommand: true,
			Action: func(context.Context, *cli.Command) error {
				t.Fatal("a suggested correction executed")
				return nil
			},
		}
	}
	hidden := leaf("__internal")
	hidden.Hidden = true
	return &cli.Command{
		Name: "openai", Suggest: true, HideHelpCommand: true,
		Writer: io.Discard, ErrWriter: io.Discard,
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "api-key"},
			&cli.StringFlag{Name: "base-url"},
		},
		Commands: []*cli.Command{
			{Name: "models", Suggest: true, HideHelpCommand: true,
				Commands: []*cli.Command{leaf("list", "ls"), leaf("retrieve"), hidden}},
			{Name: "admin", Suggest: true, HideHelpCommand: true,
				Commands: []*cli.Command{{Name: "organization", Suggest: true, HideHelpCommand: true,
					Commands: []*cli.Command{{Name: "projects", Suggest: true, HideHelpCommand: true,
						Commands: []*cli.Command{leaf("list")}}}}}},
			{Name: "audio:transcriptions", Hidden: true, Suggest: true, HideHelpCommand: true,
				Metadata: map[string]any{"command-compatibility-alias": true},
				Commands: []*cli.Command{leaf("create")}},
		},
	}
}

func TestCommandRecoveryParsedCommands(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"root suffix", []string{"modles", "list"}, "Unknown command. Did you mean: openai models list?"},
		{"nested suffix", []string{"admin", "organizaton", "projects", "list"}, "Unknown command. Did you mean: openai admin organization projects list?"},
		{"leaf typo", []string{"models", "lsit"}, "Unknown command. Did you mean: openai models list?"},
		{"canonical alias", []string{"modles", "ls"}, "Unknown command. Did you mean: openai models list?"},
		{"exact suffix only", []string{"modles", "LIST"}, "Unknown command. Did you mean: openai models?"},
		{"root fallback", []string{"zzzzzzzzzz"}, "Unknown command. Run openai help to see commands."},
		{"nested fallback", []string{"models", "zzzzzzzzzz"}, "Unknown command. Run openai help models to see commands."},
		{"parsed help", []string{"modles", "list", "--help"}, "Unknown help topic. Did you mean: openai help models list?"},
		{"nested parsed help", []string{"models", "lsit", "-h"}, "Unknown help topic. Did you mean: openai help models list?"},
		{"false help", []string{"modles", "list", "--help=false"}, "Unknown command. Did you mean: openai models list?"},
		{"help as value", []string{"modles", "--api-key", "--help", "list"}, "Unknown command. Did you mean: openai models list?"},
		{"command as value", []string{"modles", "--api-key", "list"}, "Unknown command. Did you mean: openai models?"},
		{"literal help", []string{"modles", "--", "--help"}, "Unknown command. Did you mean: openai models?"},
		{"hidden suffix", []string{"modles", "__internal"}, "Unknown command. Did you mean: openai models?"},
		{"legacy suffix", []string{"audio:transcriptins", "create"}, "Unknown command. Did you mean: openai audio:transcriptions create?"},
		{"private operand", []string{"modles", "retrieve", "/synthetic-private-home/key"}, "Unknown command. Did you mean: openai models retrieve?"},
		{"credentialed URL", []string{"modles", "list", "--base-url", "https://synthetic-private-user:synthetic-private-password@example.invalid"}, "Unknown command. Did you mean: openai models list?"},
		{"controls", []string{"modles", "list", "\x1b]52;c;synthetic-private-secret\a"}, "Unknown command. Did you mean: openai models list?"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := recoveryCommandFixture(t)
			err := root.Run(t.Context(), append([]string{"openai"}, test.args...))
			var exit cli.ExitCoder
			if !errors.As(err, &exit) || exit.ExitCode() != 3 {
				t.Fatalf("expected unknown-command status 3, got %v", err)
			}
			if got := commandRecoveryMessage(root); got != test.want {
				t.Fatalf("recovery = %q; want %q", got, test.want)
			}
		})
	}
}

func TestCommandRecoveryHelpSuffixBoundaries(t *testing.T) {
	for _, test := range []struct {
		remaining []string
		want      string
	}{
		{[]string{"projects", "list"}, "openai help admin organization projects list"},
		{[]string{"projects", "list", "synthetic-private-operand"}, "openai help admin organization projects list"},
		{[]string{"projects", "--api-key", "list"}, "openai help admin organization projects"},
		{[]string{"synthetic-private-operand", "projects", "list"}, "openai help admin organization"},
		{[]string{"projects", "\x1b[2Jsynthetic-private-operand"}, "openai help admin organization projects"},
	} {
		t.Run(strings.Join(test.remaining, "/"), func(t *testing.T) {
			root := recoveryCommandFixture(t)
			if err := root.Run(t.Context(), []string{"openai", "zzzzzzzzzz"}); err == nil {
				t.Fatal("expected an unknown command")
			}
			got := commandRecoveryAt(root.Command("admin"), "organizaton", test.remaining, true)
			want := "Unknown help topic. Did you mean: " + test.want + "?"
			if got != want {
				t.Fatalf("recovery = %q; want %q", got, want)
			}
		})
	}
}

func TestCommandRecoveryPreservesInvocation(t *testing.T) {
	root := recoveryCommandFixture(t)
	root.Metadata = map[string]any{"help-invocation": "'/synthetic folder/openai'"}
	if err := root.Run(t.Context(), []string{"openai", "modles", "list"}); err == nil {
		t.Fatal("expected an unknown command")
	}
	const want = "Unknown command. Did you mean: '/synthetic folder/openai' models list?"
	if got := commandRecoveryMessage(root); got != want {
		t.Fatalf("recovery = %q; want %q", got, want)
	}
}

func TestCommandRecoveryWithoutSuggestion(t *testing.T) {
	for _, helpRequested := range []bool{false, true} {
		prefix := "Unknown command."
		if helpRequested {
			prefix = "Unknown help topic."
		}
		if got := commandRecoveryAt(nil, "synthetic-private-input", nil, helpRequested); got != prefix+" Run openai help to see commands." {
			t.Fatalf("nil command recovery = %q", got)
		}
		root := recoveryCommandFixture(t)
		root.Suggest = false
		if got := commandRecoveryAt(root, "modles", []string{"list"}, helpRequested); got != prefix+" Run openai help to see commands." {
			t.Fatalf("disabled suggestion recovery = %q", got)
		}
	}
}
