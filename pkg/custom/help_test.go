package custom

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestHelpFullReferenceIncludesFutureFlags(t *testing.T) {
	var out bytes.Buffer
	leaf := &cli.Command{
		Name: "generate", CustomHelpTemplate: "short image help\n",
		Flags: []cli.Flag{&cli.StringFlag{Name: "future-api-option", Usage: "A newly generated API flag"}},
		Before: func(ctx context.Context, _ *cli.Command) (context.Context, error) {
			t.Fatal("help ran request setup")
			return ctx, nil
		},
		Action: func(context.Context, *cli.Command) error { t.Fatal("help ran an API action"); return nil },
	}
	root := &cli.Command{
		Name: "openai", Writer: &out, HideHelpCommand: true,
		Flags:    []cli.Flag{&cli.StringFlag{Name: "future-global-option", Usage: "A new global flag"}},
		Commands: []*cli.Command{{Name: "images", Commands: []*cli.Command{leaf}}},
	}
	args, help, err := ConfigureHelp(root, []string{"openai", "help", "--all", "images", "generate"})
	if err != nil || !help {
		t.Fatalf("ConfigureHelp = %q, %v, %v", args, help, err)
	}
	if err := root.Run(t.Context(), args); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"openai images generate", "--future-api-option", "--future-global-option"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("full help missing %q: %s", want, out.String())
		}
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
			got, help, err := ConfigureHelp(root, args)
			if err != nil || help || !reflect.DeepEqual(got, args) {
				t.Fatalf("request changed: %q, %v, %v", got, help, err)
			}
		})
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
			got, help, err := ConfigureHelp(root, args)
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
		{"/tmp/\x1b[2J/openai", "openai"},
		{"/tmp/new\nline/openai", "openai"},
		{"unrelated-executable", "openai"},
	} {
		if got := helpInvocation("openai", []string{tc.input}); got != tc.want {
			t.Errorf("helpInvocation(%q) = %q; want %q", tc.input, got, tc.want)
		}
	}
}
