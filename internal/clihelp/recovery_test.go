package clihelp

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
		{"root suffix", []string{"modles", "list"}, "Unknown command. Run this help command to check the spelling.\n  openai help models list\nEdit your original command and run it again with the same options and arguments."},
		{"nested suffix", []string{"admin", "organizaton", "projects", "list"}, "Unknown command. Run this help command to check the spelling.\n  openai help admin organization projects list\nEdit your original command and run it again with the same options and arguments."},
		{"leaf typo", []string{"models", "lsit"}, "Unknown command. Run this help command to check the spelling.\n  openai help models list\nEdit your original command and run it again with the same options and arguments."},
		{"canonical alias", []string{"modles", "ls"}, "Unknown command. Run this help command to check the spelling.\n  openai help models list\nEdit your original command and run it again with the same options and arguments."},
		{"exact suffix only", []string{"modles", "LIST"}, "Unknown command. Run this help command to check the spelling.\n  openai help models\nEdit your original command and run it again with the same options and arguments."},
		{"root fallback", []string{"zzzzzzzzzz"}, "Unknown command. Run openai help to see commands."},
		{"nested fallback", []string{"models", "zzzzzzzzzz"}, "Unknown command. Run openai help models to see commands."},
		{"parsed help", []string{"modles", "list", "--help"}, "Unknown help topic. Run this help command to check the spelling.\n  openai help models list"},
		{"nested parsed help", []string{"models", "lsit", "-h"}, "Unknown help topic. Run this help command to check the spelling.\n  openai help models list"},
		{"false help", []string{"modles", "list", "--help=false"}, "Unknown command. Run this help command to check the spelling.\n  openai help models list\nEdit your original command and run it again with the same options and arguments."},
		{"help as value", []string{"modles", "--api-key", "--help", "list"}, "Unknown command. Run this help command to check the spelling.\n  openai help models list\nEdit your original command and run it again with the same options and arguments."},
		{"command as value", []string{"modles", "--api-key", "list"}, "Unknown command. Run this help command to check the spelling.\n  openai help models\nEdit your original command and run it again with the same options and arguments."},
		{"literal help", []string{"modles", "--", "--help"}, "Unknown command. Run this help command to check the spelling.\n  openai help models\nEdit your original command and run it again with the same options and arguments."},
		{"hidden suffix", []string{"modles", "__internal"}, "Unknown command. Run this help command to check the spelling.\n  openai help models\nEdit your original command and run it again with the same options and arguments."},
		{"legacy suffix", []string{"audio:transcriptins", "create"}, "Unknown command. Run this help command to check the spelling.\n  openai help audio:transcriptions create\nEdit your original command and run it again with the same options and arguments."},
		{"private operand", []string{"modles", "retrieve", "/synthetic-private-home/key"}, "Unknown command. Run this help command to check the spelling.\n  openai help models retrieve\nEdit your original command and run it again with the same options and arguments."},
		{"credentialed URL", []string{"modles", "list", "--base-url", "https://synthetic-private-user:synthetic-private-password@example.invalid"}, "Unknown command. Run this help command to check the spelling.\n  openai help models list\nEdit your original command and run it again with the same options and arguments."},
		{"controls", []string{"modles", "list", "\x1b]52;c;synthetic-private-secret\a"}, "Unknown command. Run this help command to check the spelling.\n  openai help models list\nEdit your original command and run it again with the same options and arguments."},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := recoveryCommandFixture(t)
			err := root.Run(t.Context(), append([]string{"openai"}, test.args...))
			var exit cli.ExitCoder
			if !errors.As(err, &exit) || exit.ExitCode() != 3 {
				t.Fatalf("expected unknown-command status 3, got %v", err)
			}
			if got := RecoveryMessage(root); got != test.want {
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
			got := RecoveryAt(root.Command("admin"), "organizaton", test.remaining, true)
			want := "Unknown help topic. Run this help command to check the spelling.\n  " + test.want
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
	const want = "Unknown command. Run this help command to check the spelling.\n  '/synthetic folder/openai' help models list\nEdit your original command and run it again with the same options and arguments."
	if got := RecoveryMessage(root); got != want {
		t.Fatalf("recovery = %q; want %q", got, want)
	}
}

func TestCommandRecoveryIdentifiesDirectoryForGoRun(t *testing.T) {
	root := recoveryCommandFixture(t)
	root.Metadata = map[string]any{"help-invocation": "go run ./cmd/openai"}
	if err := root.Run(t.Context(), []string{"openai", "model", "list"}); err == nil {
		t.Fatal("expected an unknown command")
	}
	want := "Run this help command from the CLI repository root to check the spelling.\n  go run ./cmd/openai help models list\n"
	if got := RecoveryMessage(root); !strings.Contains(got, want) {
		t.Fatalf("recovery = %q; want guidance containing %q", got, want)
	}
}

func TestCommandRecoveryWithoutSuggestion(t *testing.T) {
	for _, helpRequested := range []bool{false, true} {
		prefix := "Unknown command."
		if helpRequested {
			prefix = "Unknown help topic."
		}
		if got := RecoveryAt(nil, "synthetic-private-input", nil, helpRequested); got != prefix+" Run openai help to see commands." {
			t.Fatalf("nil command recovery = %q", got)
		}
		root := recoveryCommandFixture(t)
		root.Suggest = false
		if got := RecoveryAt(root, "modles", []string{"list"}, helpRequested); got != prefix+" Run openai help to see commands." {
			t.Fatalf("disabled suggestion recovery = %q", got)
		}
	}
}

func TestCommandRecoveryKeepsHelpTyposOutOfUnrelatedTopics(t *testing.T) {
	root := &cli.Command{Name: "openai", Suggest: true, Commands: []*cli.Command{
		{Name: "help"}, {Name: "explore"},
	}}
	for _, name := range []string{"hepl", "help", "HELP", "hel", "hlep"} {
		t.Run(name, func(t *testing.T) {
			got := RecoveryAt(root, name, nil, true)
			if want := "Unknown help topic. Run openai help to see commands."; got != want {
				t.Fatalf("recovery = %q; want %q", got, want)
			}
		})
	}
	if got := RecoveryAt(root, "explroe", nil, true); got != "Unknown help topic. Run this help command to check the spelling.\n  openai help explore" {
		t.Fatalf("explore typo lost its declared recovery: %q", got)
	}
}
