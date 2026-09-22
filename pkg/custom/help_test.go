package custom

import (
	"bytes"
	"context"
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
