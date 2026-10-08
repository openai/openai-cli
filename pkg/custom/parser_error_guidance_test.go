package custom

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

func TestParserErrorGuidanceTypes(t *testing.T) {
	for _, test := range []struct {
		name string
		flag cli.Flag
		want string
	}{
		{"integer", &cli.IntFlag{Name: "value"}, "Expected an integer."},
		{"unsigned integer", &cli.UintFlag{Name: "value"}, "Expected an integer."},
		{"number", &cli.FloatFlag{Name: "value"}, "Expected a number."},
		{"boolean", &cli.BoolFlag{Name: "value"}, "Expected a boolean (true or false)."},
		{"nullable integer", &requestflag.Flag[*int64]{Name: "value"}, "Expected an integer."},
		{"nullable number", &requestflag.Flag[*float64]{Name: "value"}, "Expected a number."},
		{"nullable boolean", &requestflag.Flag[*bool]{Name: "value"}, "Expected a boolean (true or false)."},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := &cli.Command{Name: "openai", Flags: []cli.Flag{test.flag}}
			got := localErrorMessage(root, errors.New(`invalid value "synthetic-private-value" for flag -value: synthetic-private-reason`))
			want := "Invalid value for --value. " + test.want + "\nOptions and examples: openai --help"
			if got != want {
				t.Errorf("message = %q, want %q", got, want)
			}
		})
	}
	if got := localErrorMessage(nil, errors.New("flag provided but not defined: -synthetic-private-flag")); got != "An option is not recognized.\nOptions and examples: openai --help" {
		t.Errorf("nil command diagnostic = %q", got)
	}
}

func TestParserErrorSuggestionsRespectScope(t *testing.T) {
	for _, test := range []struct {
		name, argument, suggestion string
	}{
		{"transposition", "max-itmes", "--max-items"},
		{"insertion", "max-iteems", "--max-items"},
		{"deletion", "max-ites", "--max-items"},
		{"substitution", "max-itemz", "--max-items"},
		{"uppercase", "MAX-ITEMS", "--max-items"},
		{"alias", "coutn", "--max-items"},
		{"inherited", "formta-error", "--format-error"},
		{"shadowed alias", "formta", ""},
		{"ancestor local", "api-ke", ""},
		{"hidden", "hiden", ""},
		{"ambiguous", "mile", ""},
		{"secret", "synthetic-private-token", ""},
		{"control", "max-items\x1b", ""},
		{"line break", "max-items\n", ""},
		{"suffix", "max-items=synthetic-private-value", ""},
		{"short unknown", "Z", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			leaf := &cli.Command{Name: "list", Flags: []cli.Flag{
				&cli.IntFlag{Name: "max-items", Aliases: []string{"count", "counts"}},
				&cli.StringFlag{Name: "file", Aliases: []string{"f"}},
				&cli.StringFlag{Name: "tile"},
				&cli.StringFlag{Name: "hidden", Hidden: true},
			}}
			root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "format", Aliases: []string{"f"}},
					&cli.StringFlag{Name: "format-error"},
					&requestflag.Flag[string]{Name: "api-key"},
				}, Commands: []*cli.Command{{Name: "models", Commands: []*cli.Command{leaf}}},
			}
			ConfigureCommandErrors(root)
			err := root.Run(t.Context(), []string{"openai", "models", "list", "--" + test.argument})
			if err == nil {
				t.Fatal("expected parser failure")
			}
			got := localErrorMessage(root, err)
			if !strings.Contains(got, "Options and examples: openai models list --help") {
				t.Errorf("missing scoped recovery command: %q", got)
			}
			if test.suggestion == "" {
				if strings.Contains(got, "Did you mean") {
					t.Errorf("unexpected suggestion: %q", got)
				}
			} else if !strings.Contains(got, "Did you mean "+test.suggestion+"?") {
				t.Errorf("missing suggestion %q: %q", test.suggestion, got)
			}
			if strings.Contains(got, "synthetic-private-") || strings.ContainsAny(got, "\x1b\r") {
				t.Errorf("diagnostic exposed rejected input: %q", got)
			}
		})
	}
}

func TestParserErrorTypeUsesChildFlag(t *testing.T) {
	leaf := &cli.Command{Name: "create", Flags: []cli.Flag{&cli.IntFlag{Name: "project", Aliases: []string{"p"}}}}
	root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
		Flags: []cli.Flag{&cli.StringFlag{Name: "project", Aliases: []string{"p"}}}, Commands: []*cli.Command{leaf}}
	ConfigureCommandErrors(root)
	err := root.Run(t.Context(), []string{"openai", "create", "-p", "synthetic-private-value"})
	got := localErrorMessage(root, err)
	if got != "Invalid value for --project. Expected an integer.\nOptions and examples: openai create --help" {
		t.Errorf("shadowed flag diagnostic = %q", got)
	}
}
