package app

import (
	"context"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

type hiddenNestedErrorFlag struct{ *requestflag.InnerFlag[bool] }

func (*hiddenNestedErrorFlag) IsVisible() bool { return false }

type localNestedErrorFlag struct{ *requestflag.InnerFlag[bool] }

func (*localNestedErrorFlag) IsLocal() bool { return true }

type hiddenNestedOuterErrorFlag struct {
	*requestflag.InnerFlag[map[string]any]
}

func (*hiddenNestedOuterErrorFlag) IsVisible() bool { return false }

// The framework formats parser causes with %v. Observe Set without changing its behavior.
type observedNestedAliasErrorFlag struct {
	*requestflag.InnerFlag[bool]
	failure error
}

func (flag *observedNestedAliasErrorFlag) Set(name, value string) error {
	err := flag.InnerFlag.Set(name, value)
	if err != nil {
		flag.failure = err
	}
	return err
}

type observedOrdinaryAliasErrorFlag struct {
	*cli.IntFlag
	failure error
}

func (flag *observedOrdinaryAliasErrorFlag) Set(name, value string) error {
	err := flag.IntFlag.Set(name, value)
	if err != nil {
		flag.failure = err
	}
	return err
}

func TestNestedParserErrorGuidanceRequiresVisibleOuterIdentity(t *testing.T) {
	for _, scope := range []string{"visible outer", "hidden outer", "hidden inner", "absent outer", "different outer", "ancestor local", "shadowed inherited alias", "multilevel chain", "hidden intermediate", "absent intermediate", "nil outer", "cycle"} {
		t.Run(scope, func(t *testing.T) {
			outer := &requestflag.Flag[map[string]any]{Name: "settings", BodyPath: "settings"}
			inner := &requestflag.InnerFlag[bool]{Name: "settings.enabled", InnerField: "enabled", OuterFlag: outer}
			var declared cli.Flag = inner
			leaf := &cli.Command{Name: "create", Flags: []cli.Flag{outer, inner}}
			root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard, Commands: []*cli.Command{leaf}}
			visible := scope == "visible outer" || scope == "shadowed inherited alias" || scope == "multilevel chain"
			switch scope {
			case "hidden outer":
				outer.Hidden = true
			case "hidden inner":
				declared = &hiddenNestedErrorFlag{inner}
				leaf.Flags = []cli.Flag{outer, declared}
			case "absent outer":
				leaf.Flags = []cli.Flag{inner}
			case "different outer":
				copy := *outer
				leaf.Flags = []cli.Flag{&copy, inner}
			case "ancestor local":
				declared = &localNestedErrorFlag{inner}
				root.Flags, leaf.Flags = []cli.Flag{outer, declared}, nil
			case "shadowed inherited alias":
				inner.Aliases = []string{"p"}
				root.Flags = []cli.Flag{&cli.StringFlag{Name: "project", Aliases: []string{"p"}}}
			case "multilevel chain", "hidden intermediate", "absent intermediate", "cycle":
				middle := &requestflag.InnerFlag[map[string]any]{Name: "settings.child", InnerField: "child", OuterFlag: outer}
				inner.Name, inner.OuterFlag = "settings.child.enabled", middle
				// Put the leaf first so visibility does not depend on declaration order.
				leaf.Flags = []cli.Flag{inner, outer, middle}
				if scope == "hidden intermediate" {
					hidden := &hiddenNestedOuterErrorFlag{middle}
					inner.OuterFlag = hidden
					leaf.Flags = []cli.Flag{inner, outer, hidden}
				} else if scope == "absent intermediate" {
					leaf.Flags = []cli.Flag{inner, outer}
				} else if scope == "cycle" {
					middle.OuterFlag = inner
				}
			case "nil outer":
				inner.OuterFlag = nil
			}
			actionRan := false
			leaf.Action = func(context.Context, *cli.Command) error { actionRan = true; return nil }
			ConfigureCommandErrors(root)
			typo := inner.Name + "d"
			err := root.Run(t.Context(), []string{"openai", "create", "--" + typo + "=synthetic-private-value"})
			if err == nil || actionRan {
				t.Fatalf("unknown nested flag reached the action: err=%v, action=%t", err, actionRan)
			}
			flags := parserErrorFlags(leaf)
			if got := slices.Contains(flags, declared); got != visible {
				t.Fatalf("nested declaration membership=%t, want %t", got, visible)
			}
			count := 0
			for _, flag := range flags {
				if flag == declared {
					count++
				}
			}
			if count > 1 {
				t.Fatalf("nested declaration appears %d times", count)
			}
			message := "An option is not recognized."
			if visible {
				message += " Did you mean --" + inner.Name + "?"
				if got := parserFlagExpectedType(leaf, inner.Name); got != "a boolean (true or false)" {
					t.Fatalf("nested boolean lost its expected type: %q", got)
				}
			} else {
				if got := suggestParserFlag(leaf, typo); got != "" {
					t.Fatalf("suggested a hidden or unavailable nested flag: %q", got)
				}
			}
			message += "\nOptions and examples: openai create --help"
			if got := localErrorMessage(root, err); got != message || strings.Contains(got, "synthetic-private") {
				t.Fatalf("wrong nested guidance: got %q, want %q", got, message)
			}
			if scope == "shadowed inherited alias" {
				if got := parserFlagExpectedType(leaf, "p"); got != "a boolean (true or false)" {
					t.Fatalf("inherited flag overrode nested alias type: %q", got)
				}
				if got := suggestParserFlag(leaf, "projec"); got != "" {
					t.Fatalf("suggested a shadowed inherited flag: %q", got)
				}
			}
		})
	}
}

func TestNestedParserErrorGuidanceTypes(t *testing.T) {
	for _, tc := range []struct {
		name, expected string
		flag           func(cli.Flag) cli.Flag
	}{
		{"integer", "an integer, such as 1", func(outer cli.Flag) cli.Flag {
			return &requestflag.InnerFlag[int64]{Name: "settings.value", Aliases: []string{"v"}, InnerField: "value", OuterFlag: outer}
		}},
		{"number", "a number, such as 0.5", func(outer cli.Flag) cli.Flag {
			return &requestflag.InnerFlag[float64]{Name: "settings.value", Aliases: []string{"v"}, InnerField: "value", OuterFlag: outer}
		}},
		{"boolean", "a boolean (true or false)", func(outer cli.Flag) cli.Flag {
			return &requestflag.InnerFlag[bool]{Name: "settings.value", Aliases: []string{"v"}, InnerField: "value", OuterFlag: outer}
		}},
		{"nullable integer", "an integer, such as 1", func(outer cli.Flag) cli.Flag {
			return &requestflag.InnerFlag[*int64]{Name: "settings.value", Aliases: []string{"v"}, InnerField: "value", OuterFlag: outer}
		}},
		{"nullable number", "a number, such as 0.5", func(outer cli.Flag) cli.Flag {
			return &requestflag.InnerFlag[*float64]{Name: "settings.value", Aliases: []string{"v"}, InnerField: "value", OuterFlag: outer}
		}},
		{"nullable boolean", "a boolean (true or false)", func(outer cli.Flag) cli.Flag {
			return &requestflag.InnerFlag[*bool]{Name: "settings.value", Aliases: []string{"v"}, InnerField: "value", OuterFlag: outer}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outer := &requestflag.Flag[map[string]any]{Name: "settings", BodyPath: "settings"}
			flag := tc.flag(outer)
			leaf := &cli.Command{Name: "create", Flags: []cli.Flag{outer, flag}}
			actionRan := false
			leaf.Action = func(context.Context, *cli.Command) error { actionRan = true; return nil }
			root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard, Commands: []*cli.Command{leaf}}
			ConfigureCommandErrors(root)
			err := root.Run(t.Context(), []string{"openai", "create", "--settings.value=synthetic-private-invalid"})
			if err == nil || actionRan {
				t.Fatalf("invalid nested value reached the action: %v", err)
			}
			want := "Invalid value for --settings.value. Expected " + tc.expected + ".\nOptions and examples: openai create --help"
			if got := localErrorMessage(root, err); got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
			if got := parserFlagExpectedType(leaf, "v"); got != tc.expected {
				t.Fatalf("alias type=%q, want %q", got, tc.expected)
			}
		})
	}
}

func TestNestedParserErrorGuidancePreservesLocalAliasPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		innerFirst, ordinaryHidden bool
	}{
		{"inner before ordinary", true, false},
		{"ordinary before inner", false, false},
		{"hidden ordinary before inner", false, true},
		{"inner before hidden ordinary", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outer := &requestflag.Flag[map[string]any]{Name: "settings", BodyPath: "settings"}
			inner := &observedNestedAliasErrorFlag{InnerFlag: &requestflag.InnerFlag[bool]{Name: "settings.enabled", Aliases: []string{"v"}, InnerField: "enabled", OuterFlag: outer}}
			ordinary := &observedOrdinaryAliasErrorFlag{IntFlag: &cli.IntFlag{Name: "count", Aliases: []string{"v"}, Hidden: tc.ordinaryHidden}}
			flags := []cli.Flag{outer, ordinary, inner}
			if tc.innerFirst {
				ordinary.Aliases = nil
				flags = []cli.Flag{outer, inner, ordinary}
			} else {
				inner.Aliases = nil
			}
			inherited := &cli.FloatFlag{Name: "fallback", Aliases: []string{"v"}}
			actionRan := false
			leaf := &cli.Command{Name: "create", Flags: flags, Action: func(context.Context, *cli.Command) error { actionRan = true; return nil }}
			root := &cli.Command{Name: "openai", Flags: []cli.Flag{inherited}, Writer: io.Discard, ErrWriter: io.Discard, Commands: []*cli.Command{leaf}}
			ConfigureCommandErrors(root)
			err := root.Run(t.Context(), []string{"openai", "create", "-v=synthetic-private-invalid"})
			if err == nil || actionRan {
				t.Fatalf("invalid alias value reached the action: err=%v, action=%t", err, actionRan)
			}
			if inherited.IsSet() {
				t.Fatal("inherited number parser received the local alias value")
			}
			var winner cli.Flag
			var parseFailure error
			if inner.failure != nil {
				winner, parseFailure = inner, inner.failure
			}
			if ordinary.failure != nil {
				if winner != nil {
					t.Fatal("two declarations rejected one alias value")
				}
				winner, parseFailure = ordinary, ordinary.failure
			}
			var expectedWinner cli.Flag = ordinary
			if tc.innerFirst {
				expectedWinner = inner
			}
			if winner != expectedWinner {
				t.Fatalf("alias parser owner=%T, want %T", winner, expectedWinner)
			}
			var parsed *strconv.NumError
			if !errors.As(parseFailure, &parsed) {
				t.Fatalf("alias parse cause is unavailable: %T %v", parseFailure, parseFailure)
			}
			option := "--" + winner.Names()[0]
			var expected string
			switch parsed.Func {
			case "ParseBool":
				expected = "a boolean (true or false)"
			case "ParseInt":
				expected = "an integer, such as 1"
			default:
				t.Fatalf("unexpected winning parser: %s", parsed.Func)
			}
			if winner == ordinary && tc.ordinaryHidden {
				// Direct hidden-option naming is preserved, but its visible loser must not supply a type.
				expected = ""
			}
			t.Logf("alias -v used %s for %s", parsed.Func, option)
			want := "Invalid value for " + option + "."
			if expected != "" {
				want += " Expected " + expected + "."
			}
			want += "\nOptions and examples: openai create --help"
			if got := localErrorMessage(root, err); got != want {
				t.Fatalf("guidance disagrees with the winning alias parser: got %q, want %q", got, want)
			}
			if got := parserFlagExpectedType(leaf, "v"); got != expected {
				t.Fatalf("alias type=%q, actual parser requires %q", got, expected)
			}
		})
	}
}

func TestNestedParserErrorGuidanceRejectsDuplicateLocalAliases(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		innerFirst, ordinaryHidden bool
	}{
		{"inner before ordinary", true, false},
		{"ordinary before inner", false, false},
		{"hidden ordinary before inner", false, true},
		{"inner before hidden ordinary", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outer := &requestflag.Flag[map[string]any]{Name: "settings", BodyPath: "settings"}
			inner := &observedNestedAliasErrorFlag{InnerFlag: &requestflag.InnerFlag[bool]{Name: "settings.enabled", Aliases: []string{"v"}, InnerField: "enabled", OuterFlag: outer}}
			ordinary := &observedOrdinaryAliasErrorFlag{IntFlag: &cli.IntFlag{Name: "count", Aliases: []string{"v"}, Hidden: tc.ordinaryHidden}}
			flags := []cli.Flag{outer, ordinary, inner}
			if tc.innerFirst {
				flags = []cli.Flag{outer, inner, ordinary}
			}
			actionRan := false
			leaf := &cli.Command{Name: "create", Flags: flags, Action: func(context.Context, *cli.Command) error { actionRan = true; return nil }}
			root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard, Commands: []*cli.Command{leaf}}
			ConfigureCommandErrors(root)
			err := root.Run(t.Context(), []string{"openai", "create", "-v=synthetic-private-invalid"})
			want := `flag "v" defined multiple times in command "openai create"`
			if err == nil || err.Error() != want {
				t.Fatalf("duplicate alias rejection=%v, want %q", err, want)
			}
			if actionRan || inner.failure != nil || ordinary.failure != nil {
				t.Fatalf("duplicate aliases reached parsing or action: inner=%v, ordinary=%v, action=%t", inner.failure, ordinary.failure, actionRan)
			}
		})
	}
}
