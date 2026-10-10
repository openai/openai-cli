package custom

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

func TestUnavailableParserFlagUsesPublicDeclarations(t *testing.T) {
	leaf := &cli.Command{Name: "list", Flags: []cli.Flag{
		&cli.StringFlag{Name: "shared", Aliases: []string{"s"}},
	}, Action: func(context.Context, *cli.Command) error { return nil }}
	root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
		Flags: []cli.Flag{&cli.StringFlag{Name: "format"}, &requestflag.Flag[string]{Name: "root-only"}},
		Commands: []*cli.Command{
			{Name: "models", Commands: []*cli.Command{leaf}},
			{Name: "files", Flags: []cli.Flag{&cli.StringFlag{Name: "file", Aliases: []string{"f"}}, &cli.StringFlag{Name: "shared"}, &cli.StringFlag{Name: "private-flag", Hidden: true}}},
			{Name: "another", Flags: []cli.Flag{&cli.StringFlag{Name: "formatting", Aliases: []string{"f"}}}},
			{Name: "hidden", Hidden: true, Flags: []cli.Flag{&cli.StringFlag{Name: "hidden-only"}}, Commands: []*cli.Command{{Name: "visible-child", Flags: []cli.Flag{&cli.StringFlag{Name: "hidden-child-only"}}}}},
		},
	}
	ConfigureCommandErrors(root)
	if err := root.Run(t.Context(), []string{"openai", "models", "list"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ provided, want string }{
		{"file", "--file"}, {"f", "-f"}, {"root-only", "--root-only"},
		{"shared", ""}, {"s", ""}, {"format", ""}, {"private-flag", ""}, {"hidden-only", ""}, {"hidden-child-only", ""},
		{"fil", ""}, {"FILE", ""}, {"file=synthetic-private-value", ""}, {"file\x1b[31m", ""}, {"synthetic-private-name", ""},
	} {
		t.Run(tc.provided, func(t *testing.T) {
			if got := unavailableParserFlag(leaf, tc.provided); got != tc.want {
				t.Fatalf("got %q; want %q", got, tc.want)
			}
		})
	}
	if got := unavailableParserFlag(nil, "file"); got != "" {
		t.Fatalf("nil scope: %q", got)
	}
}

func TestUnavailableParserFlagKeepsLocalTypoSuggestion(t *testing.T) {
	leaf := &cli.Command{Name: "create", Flags: []cli.Flag{&cli.StringFlag{Name: "model"}}}
	root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard, Commands: []*cli.Command{
		leaf, {Name: "elsewhere", Flags: []cli.Flag{&cli.StringFlag{Name: "mode"}}},
	}}
	ConfigureCommandErrors(root)
	err := root.Run(t.Context(), []string{"openai", "create", "--mode=synthetic-private-value"})
	if err == nil {
		t.Fatal("expected parser failure")
	}
	want := "The --mode option is not available for this command. Did you mean --model?\nOptions and examples: openai create --help"
	if got := localErrorMessage(root, err); got != want {
		t.Fatalf("got %q; want %q", got, want)
	}
}

func TestUnavailableParserFlagPreservesAliasShadowing(t *testing.T) {
	leaf := &cli.Command{Name: "create", Flags: []cli.Flag{&cli.IntFlag{Name: "project", Aliases: []string{"p"}}}}
	root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
		Flags: []cli.Flag{&cli.StringFlag{Name: "project", Aliases: []string{"p"}}}, Commands: []*cli.Command{leaf},
	}
	ConfigureCommandErrors(root)
	err := root.Run(t.Context(), []string{"openai", "create", "-p", "synthetic-private-value"})
	if err == nil {
		t.Fatal("expected integer parsing failure")
	}
	got := localErrorMessage(root, err)
	if !strings.Contains(got, "Invalid value for --project. Expected an integer.") || strings.Contains(got, "not available") {
		t.Fatalf("wrong local flag classification: %q", got)
	}
	for _, name := range []string{"project", "p"} {
		if got := unavailableParserFlag(leaf, name); got != "" {
			t.Fatalf("valid shadowed flag classified as unavailable: %q", got)
		}
	}
	if got := localErrorMessage(root, errors.New("flag provided but not defined: -synthetic-private-name")); strings.Contains(got, "synthetic-private") {
		t.Fatal(got)
	}
}

func TestUnavailableParserFlagPreservesDifferentNameAliasShadowing(t *testing.T) {
	leaf := &cli.Command{Name: "create", Flags: []cli.Flag{&cli.IntFlag{Name: "count", Aliases: []string{"p"}}}}
	root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
		Flags: []cli.Flag{&cli.StringFlag{Name: "project", Aliases: []string{"p"}}}, Commands: []*cli.Command{leaf},
	}
	ConfigureCommandErrors(root)
	err := root.Run(t.Context(), []string{"openai", "create", "-p", "synthetic-private-value"})
	if err == nil {
		t.Fatal("expected integer parsing failure")
	}
	got := localErrorMessage(root, err)
	if !strings.Contains(got, "Invalid value for --count. Expected an integer.") || strings.Contains(got, "not available") {
		t.Fatalf("wrong local alias classification: %q", got)
	}
	for _, name := range []string{"count", "p"} {
		if got := unavailableParserFlag(leaf, name); got != "" {
			t.Fatalf("valid flag classified as unavailable: %q", got)
		}
	}
	if got := unavailableParserFlag(leaf, "project"); got != "--project" {
		t.Fatalf("shadowed ancestor flag: %q", got)
	}
}
