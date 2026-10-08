package clihelp

import (
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

func TestSynopsisShowsRequiredInputsAndStdinKeys(t *testing.T) {
	command := &cli.Command{Name: "create", Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "file", Required: true, FileInput: true, BodyPath: "file"},
		&requestflag.Flag[string]{Name: "file-purpose", Required: true, BodyPath: "purpose"},
		&cli.StringFlag{Name: "required-cli", Required: true},
		&cli.IntFlag{Name: "limit"},
		&cli.BoolFlag{Name: "hidden", Hidden: true},
	}}
	got := commandSynopsis(command, "openai files create", "openai", 64)
	for _, want := range []string{"[--file PATH]", "[--file-purpose TEXT]", "--required-cli TEXT", "[--limit INTEGER]"} {
		if !strings.Contains(got, want) {
			t.Errorf("synopsis lost %q: %s", want, got)
		}
	}
	if strings.Contains(got, "[--required-cli") || strings.Contains(got, "hidden") {
		t.Fatalf("required or hidden CLI input changed: %s", got)
	}
	note := synopsisInputNote(command)
	for _, want := range []string{"Required request inputs: --file, --file-purpose.", "flags or piped JSON/YAML", "JSON/YAML keys: file, purpose."} {
		if !strings.Contains(note, want) {
			t.Errorf("required input note lost %q: %s", want, note)
		}
	}
}

func TestSynopsisUsesOnlyVerifiedPositionalAliases(t *testing.T) {
	model := &requestflag.Flag[string]{Name: "model", PathParam: "model", Required: true}
	other := &requestflag.Flag[string]{Name: "other", PathParam: "other_id", Required: true}
	command := &cli.Command{Name: "retrieve", Flags: []cli.Flag{model, other,
		&cli.StringFlag{Name: "format"},
		&requestflag.Flag[string]{Name: "hidden", PathParam: "hidden", Hidden: true},
	}, Metadata: map[string]any{"help-positional-flags": []string{"model", "format", "unknown", "hidden", "model"}}}
	got := commandSynopsis(command, "openai models retrieve", "openai", 100)
	if strings.Count(got, "[MODEL | --model MODEL]") != 1 || !strings.Contains(got, "[--other TEXT]") {
		t.Fatalf("positional metadata did not match visible path flags: %s", got)
	}
	for _, absent := range []string{"FORMAT |", "unknown", "hidden", "TEXT | --other"} {
		if strings.Contains(got, absent) {
			t.Fatalf("invented positional argument %q: %s", absent, got)
		}
	}
	if !strings.Contains(synopsisInputNote(command), "positional arguments can replace their matching flags") {
		t.Fatal("positional source was not explained")
	}
}

func TestSynopsisOmitsNestedFieldsOnlyWithVisibleParent(t *testing.T) {
	outer := &requestflag.Flag[map[string]any]{Name: "settings"}
	inner := &visibleSynopsisInnerFlag{InnerFlag: &requestflag.InnerFlag[string]{Name: "settings.mode", OuterFlag: outer}}
	command := &cli.Command{Name: "create", Flags: []cli.Flag{outer, inner}}
	got := commandSynopsis(command, "openai items create", "openai", 80)
	if !strings.Contains(got, "[--settings TEXT=VALUE]") || strings.Contains(got, "settings.mode") {
		t.Fatalf("nested synopsis is redundant: %s", got)
	}
	help := commandHelpAtWidth(command, "openai", "items create", 80)
	if !strings.Contains(help, "--settings.mode TEXT") {
		t.Fatalf("nested option disappeared from the full reference: %s", help)
	}
	outer.Hidden = true
	got = commandSynopsis(command, "openai items create", "openai", 80)
	if !strings.Contains(got, "[--settings.mode TEXT]") {
		t.Fatalf("nested flag lacks a visible parent and was omitted: %s", got)
	}
}

type visibleSynopsisInnerFlag struct {
	*requestflag.InnerFlag[string]
}

func (*visibleSynopsisInnerFlag) IsVisible() bool { return true }

func TestSynopsisPreservesAuthoredUsageAndSafeInvocation(t *testing.T) {
	invocation := "'/tmp/CLI build/openai'"
	command := &cli.Command{UsageText: "openai images preview [--inline auto|on] FILE\nAn openai token inside prose stays unchanged."}
	got := commandSynopsis(command, invocation+" images preview", invocation, 40)
	want := "   " + invocation + " images preview [--inline auto|on] FILE\n   An openai token inside prose stays unchanged.\n"
	if got != want {
		t.Fatalf("authored usage changed: %q", got)
	}
	command.UsageText = ""
	command.Flags = []cli.Flag{&cli.StringFlag{Name: "first"}, &cli.StringFlag{Name: "second"}}
	got = commandSynopsis(command, invocation+" items create", invocation, 40)
	if !strings.Contains(got, invocation) || !strings.Contains(got, "\n       [--first TEXT]") || !strings.Contains(got, "[--second TEXT]") {
		t.Fatalf("synopsis split the invocation or failed to wrap options: %s", got)
	}
}
