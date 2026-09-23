package clihelp

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestHelpUsesFeatureTemplatesWithoutRequestSetup(t *testing.T) {
	for _, args := range [][]string{
		{"openai", "help", "--all", "guide", "size"},
		{"openai", "guide", "help", "size", "--all"},
		{"openai", "guide", "size"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			var out bytes.Buffer
			root := &cli.Command{
				Name: "openai", Writer: &out, HideHelpCommand: true,
				Before: func(ctx context.Context, _ *cli.Command) (context.Context, error) {
					t.Fatal("local help ran request setup")
					return ctx, nil
				},
				Commands: []*cli.Command{{Name: "guide", Commands: []*cli.Command{{
					Name: "size", HideHelpCommand: true,
					CustomHelpTemplate: "Short size guide\n",
					Metadata:           map[string]any{"local-help": true, "local-help-full": "Full size guide\n"},
				}}}},
			}
			normalized, help, err := Configure(root, args)
			if err != nil || !help {
				t.Fatalf("Configure = %q, %v, %v", normalized, help, err)
			}
			if err := root.Run(t.Context(), normalized); err != nil {
				t.Fatal(err)
			}
			want := "Short size guide\n"
			if strings.Contains(strings.Join(args, " "), "--all") {
				want = "Full size guide\n"
			}
			if got := out.String(); got != want {
				t.Fatalf("help = %q; want %q", got, want)
			}
		})
	}
}

func TestUnknownHelpTopicKeepsExitCode(t *testing.T) {
	for _, args := range [][]string{
		{"openai", "help", "missing"},
		{"openai", "guide", "help", "missing"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			root := &cli.Command{Name: "openai", HideHelpCommand: true,
				Commands:       []*cli.Command{{Name: "guide", Commands: []*cli.Command{{Name: "size"}}}},
				ExitErrHandler: func(context.Context, *cli.Command, error) {},
			}
			normalized, _, err := Configure(root, args)
			if err == nil {
				err = root.Run(t.Context(), normalized)
			}
			var exit cli.ExitCoder
			if !errors.As(err, &exit) || exit.ExitCode() != 3 {
				t.Fatalf("unknown help error = %v; want exit code 3", err)
			}
			if !strings.Contains(err.Error(), `Unknown help topic "missing"`) {
				t.Fatalf("unexpected help error: %v", err)
			}
		})
	}
}

func TestHelpDoesNotInterpretRequestValues(t *testing.T) {
	for _, args := range [][]string{
		{"openai", "images", "generate", "help"},
		{"openai", "images", "generate", "--prompt", "help", "--model", "test"},
		{"openai", "images", "generate", "--prompt", "--help-all"},
		{"openai", "images", "generate", "--prompt", "--help"},
		{"openai", "images", "generate", "--prompt", "--all"},
		{"openai", "images", "generate", "--prompt=help"},
		{"openai", "--header", "help", "images", "generate", "--prompt", "test"},
		{"openai", "images", "generate", "--", "help"},
		{"openai", "__complete", "--", "help"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			root := &cli.Command{Name: "openai", Commands: []*cli.Command{{Name: "images", Commands: []*cli.Command{{Name: "generate"}}}}}
			got, help, err := Configure(root, args)
			if err != nil || help || !reflect.DeepEqual(got, args) {
				t.Fatalf("request changed: %q, %v, %v", got, help, err)
			}
		})
	}
}

func TestHelpKeepsLiteralTopicsAfterDoubleDash(t *testing.T) {
	root := &cli.Command{Name: "openai"}
	args := []string{"openai", "help", "--", "--help"}
	got, help, err := Configure(root, args)
	if err != nil || !help || !reflect.DeepEqual(got, args) {
		t.Fatalf("literal help topic changed: %q, %v, %v", got, help, err)
	}
}

func TestHelpPreservesLeafPositionalOperands(t *testing.T) {
	for _, args := range [][]string{
		{"openai", "images", "preview", "help"},
		{"openai", "images", "preview", "help", "--open"},
		{"openai", "models", "retrieve", "help"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			var called bool
			leaf := &cli.Command{
				Name: args[2], HideHelpCommand: true,
				Flags: []cli.Flag{&cli.BoolFlag{Name: "open"}},
				Action: func(_ context.Context, command *cli.Command) error {
					called = true
					if !reflect.DeepEqual(command.Args().Slice(), []string{"help"}) {
						t.Errorf("leaf operands = %q; want [help]", command.Args().Slice())
					}
					return nil
				},
			}
			root := &cli.Command{Name: "openai", HideHelpCommand: true, Commands: []*cli.Command{{Name: args[1], Commands: []*cli.Command{leaf}}}}
			got, help, err := Configure(root, args)
			if err != nil || help || !reflect.DeepEqual(got, args) {
				t.Fatalf("leaf invocation changed: %q, %v, %v", got, help, err)
			}
			if err := root.Run(t.Context(), got); err != nil {
				t.Fatal(err)
			}
			if !called {
				t.Fatal("positional operand intercepted instead of calling leaf action")
			}
		})
	}
}

func TestHelpInvocationPreservesCopyablePaths(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"./openai", "./openai"},
		{"/tmp/my cli/openai", "'/tmp/my cli/openai'"},
		{"/tmp/user's/openai", "'/tmp/user'\\''s/openai'"},
		{"/tmp/my[1]/openai", "'/tmp/my[1]/openai'"},
		{"/tmp/a\\b/openai", "'/tmp/a\\b/openai'"},
		{"/tmp/\x1b[2J/openai", "openai"},
		{"/tmp/new\nline/openai", "openai"},
		{"unrelated-executable", "openai"},
	} {
		if runtime.GOOS == "windows" {
			if strings.HasPrefix(tc.want, "'") {
				tc.want = "& '" + strings.ReplaceAll(tc.input, "'", "''") + "'"
			}
			if tc.input == "/tmp/a\\b/openai" {
				tc.want = tc.input
			}
		}
		if got := Invocation("openai", []string{tc.input}); got != tc.want {
			t.Errorf("Invocation(%q) = %q; want %q", tc.input, got, tc.want)
		}
	}
}

func TestHelpInvocationFromSpacedWorkingDirectory(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "my cli")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(directory)
	name, want := "openai", "./openai"
	if runtime.GOOS == "windows" {
		name, want = "openai.exe", `.\openai.exe`
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := Invocation("openai", []string{path}); got != want {
		t.Fatalf("local executable = %q; want shell-independent %q", got, want)
	}
}
