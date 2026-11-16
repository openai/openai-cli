package clihelp

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func completeHelpFixture(out *bytes.Buffer) *cli.Command {
	return &cli.Command{Name: "openai", Writer: out, HideHelpCommand: true,
		Before: func(ctx context.Context, _ *cli.Command) (context.Context, error) {
			return ctx, cli.Exit("request setup must not run", 9)
		},
		Flags: []cli.Flag{&cli.StringFlag{Name: "format", Value: "auto"}, &cli.StringFlag{Name: "root-only", Local: true}},
		Commands: []*cli.Command{{Name: "images", Commands: []*cli.Command{{Name: "generate", Usage: "Generate a synthetic image.",
			Flags:  []cli.Flag{&cli.StringFlag{Name: "size"}, &cli.StringFlag{Name: "prompt", Required: true}},
			Action: func(context.Context, *cli.Command) error { return cli.Exit("action must not run", 9) },
		}, {Name: "models", Flags: []cli.Flag{&cli.BoolFlag{Name: "all"}}, Action: func(context.Context, *cli.Command) error { return nil }}}}},
	}
}

func TestHelpSpellingsSelectTheSamePage(t *testing.T) {
	references := map[bool]string{}
	for _, args := range [][]string{
		{"openai", "images", "generate", "--help"},
		{"openai", "images", "generate", "-h"},
		{"openai", "images", "generate", "--h"},
		{"openai", "help", "images", "generate"},
		{"openai", "images", "help", "generate"},
		{"openai", "--help", "images", "generate"},
		{"openai", "images", "--help", "generate"},
		{"openai", "--help", "images", "generate", "--size", "1024x1024"},
		{"openai", "images", "generate", "--size", "1024x1024", "--help"},
		{"openai", "images", "generate", "--prompt", "--help", "--help"},
		{"openai", "--format", "help", "images", "generate", "--help"},
		{"openai", "help", "--all", "images", "generate"},
		{"openai", "help", "images", "generate", "--all=false"},
		{"openai", "images", "help", "--all", "generate"},
		{"openai", "--help", "--all", "images", "generate"},
		{"openai", "-all", "--help", "images", "generate"},
		{"openai", "-all=false", "--help", "images", "generate"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			var out bytes.Buffer
			root := completeHelpFixture(&out)
			normalized, _, err := Configure(root, args)
			if err != nil {
				t.Fatal(err)
			}
			if err := root.Run(t.Context(), normalized); err != nil {
				t.Fatal(err)
			}
			got := out.String()
			full := slices.Contains(args, "--all") || slices.Contains(args, "-all")
			if strings.Contains(got, "\nGLOBAL FLAGS\n") != full || strings.Contains(got, "no longer needed") {
				t.Fatalf("incorrect help mode for %q:\n%s", args, got)
			}
			if reference, ok := references[full]; ok && got != reference {
				t.Fatalf("help differs for %q:\n%s\nwant:\n%s", normalized, got, reference)
			} else {
				references[full] = got
			}
			if strings.Contains(got, "--root-only") {
				t.Fatal("root-only flag advertised on leaf")
			}
		})
	}
}

func TestMalformedLegacyHelpFlagsRemainParserErrors(t *testing.T) {
	for _, args := range [][]string{
		{"openai", "---all", "--help"},
		{"openai", "---all=false", "--help"},
		{"openai", "----all", "--help", "images", "generate"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			var out bytes.Buffer
			root := completeHelpFixture(&out)
			root.ExitErrHandler = func(context.Context, *cli.Command, error) {}
			normalized, _, err := Configure(root, args)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(normalized, args) {
				t.Fatalf("malformed legacy flag changed: %q => %q", args, normalized)
			}
			if err := root.Run(t.Context(), normalized); err == nil {
				t.Fatal("malformed legacy flag bypassed the parser")
			}
			if strings.Contains(out.String(), "no longer needed") {
				t.Fatal("malformed legacy flag emitted a migration notice")
			}
		})
	}
}

func TestHelpScanLeavesLiteralValuesAndUnknownFlagsToParser(t *testing.T) {
	for _, args := range [][]string{
		{"openai", "images", "generate", "--prompt", "--help"},
		{"openai", "images", "generate", "--prompt=--help"},
		{"openai", "images", "generate", "--", "--help"},
		{"openai", "images", "generate", "help"},
		{"openai", "--root-only", "help", "images", "generate"},
		{"openai", "images", "generate", "--unknown", "--help"},
		{"openai", "images", "generate", "--size"},
		{"openai", "--help", "---all", "images", "generate"},
	} {
		var out bytes.Buffer
		root := completeHelpFixture(&out)
		got, help := normalizeHelpFlag(root, args)
		if help || !slices.Equal(got, args) {
			t.Fatalf("literal or invalid request changed: %q => %q, %v", args, got, help)
		}
	}
}

func TestCompleteHelpDoesNotRetireCommandOwnedAll(t *testing.T) {
	var out bytes.Buffer
	root := completeHelpFixture(&out)
	args, _, err := Configure(root, []string{"openai", "images", "models", "--all", "--help"})
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Run(t.Context(), args); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--all") || strings.Contains(out.String(), "no longer needed") {
		t.Fatalf("command-owned --all changed: %s", out.String())
	}
}

func TestSetupGuideHonorsLastExplicitHelpMode(t *testing.T) {
	var out bytes.Buffer
	root := completeHelpFixture(&out)
	for _, tc := range []struct {
		args []string
		full bool
	}{
		{[]string{"help", "--all", "setup"}, true},
		{[]string{"help", "setup"}, false},
		{[]string{"help", "--all", "--all=false", "setup"}, false},
		{[]string{"help", "--all=false", "--all", "setup"}, true},
	} {
		out.Reset()
		argv, _, err := Configure(root, append([]string{"openai"}, tc.args...))
		if err != nil {
			t.Fatal(err)
		}
		if err := root.Run(t.Context(), argv); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		if strings.Contains(got, "ALREADY HAVE A KEY?") != tc.full ||
			strings.Contains(got, "openai help --all setup") == tc.full || strings.Contains(got, "no longer needed") {
			t.Fatalf("wrong guide for %v: %s", tc.args, got)
		}
	}
}

func TestRootHelpLegacyFlagKeepsCanonicalBody(t *testing.T) {
	var want string
	for _, args := range [][]string{{"openai", "help"}, {"openai", "help", "--all=false"}} {
		var out bytes.Buffer
		root := completeHelpFixture(&out)
		argv, _, err := Configure(root, args)
		if err != nil {
			t.Fatal(err)
		}
		if err := root.Run(t.Context(), argv); err != nil {
			t.Fatal(err)
		}
		if want == "" {
			want = out.String()
		} else if line, body, found := strings.Cut(out.String(), "\n"); !found || !strings.Contains(line, "no longer needed") || body != want {
			t.Fatalf("legacy root guide differs: %s", out.String())
		}
	}
}

func TestHelpFlagKeepsLeafOperandsAndBooleanAssignments(t *testing.T) {
	var reference string
	for _, args := range [][]string{
		{"openai", "images", "generate", "--help"},
		{"openai", "--help=true", "images", "generate"},
		{"openai", "-h=true", "images", "generate"},
		{"openai", "--h=1", "images", "generate"},
		{"openai", "--help", "images", "generate", "photo.png"},
		{"openai", "images", "generate", "photo.png", "--help"},
		{"openai", "images", "generate", "photo.png", "--size", "1024x1024", "--help"},
		{"openai", "images", "generate", "--help", "--", "photo.png"},
		{"openai", "images", "generate", "--help", "--", "--help=false"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			var out bytes.Buffer
			root := completeHelpFixture(&out)
			normalized, _, err := Configure(root, args)
			if err != nil {
				t.Fatal(err)
			}
			if slices.Contains(args, "photo.png") && !slices.Contains(normalized, "photo.png") {
				t.Fatal("help removed the positional operand")
			}
			if err := root.Run(t.Context(), normalized); err != nil {
				t.Fatal(err)
			}
			if reference == "" {
				reference = out.String()
			}
			if out.String() != reference {
				t.Fatalf("help differs for %q: %s", args, out.String())
			}
		})
	}
}

func TestHelpFlagFalseAndLiteralAssignmentsRemainData(t *testing.T) {
	for _, args := range [][]string{
		{"openai", "images", "generate", "--help=false"},
		{"openai", "--help", "--help=false", "images", "generate"},
		{"openai", "images", "generate", "-h=0"},
		{"openai", "images", "generate", "--help=invalid"},
		{"openai", "images", "generate", "--prompt", "--help=true"},
		{"openai", "images", "generate", "--prompt=--help=true"},
		{"openai", "images", "generate", "photo.png", "--", "--help=true"},
	} {
		var out bytes.Buffer
		root := completeHelpFixture(&out)
		got, help := normalizeHelpFlag(root, args)
		if help || !slices.Equal(got, args) {
			t.Fatalf("false or literal help changed: %q => %q", args, got)
		}
	}
}

func TestRepeatedHelpConfigurationResetsFullReference(t *testing.T) {
	var out bytes.Buffer
	root := completeHelpFixture(&out)
	for _, args := range [][]string{
		{"openai", "help", "--all", "images", "generate"},
		{"openai", "help", "images", "generate"},
		{"openai", "images", "generate", "--help"},
		{"openai", "help", "-all", "images", "generate"},
		{"openai", "help", "images", "generate"},
		{"openai", "help", "--all=false", "images", "generate"},
		{"openai", "images", "generate", "--help"},
	} {
		out.Reset()
		normalized, _, err := Configure(root, args)
		if err != nil {
			t.Fatal(err)
		}
		if err := root.Run(t.Context(), normalized); err != nil {
			t.Fatal(err)
		}
		full := slices.Contains(args, "--all") || slices.Contains(args, "-all")
		if strings.Contains(out.String(), "\nGLOBAL FLAGS\n") != full || strings.Contains(out.String(), "no longer needed") {
			t.Fatalf("help mode leaked between invocations %q: %s", args, out.String())
		}
	}
}

func TestOneFlagHelpSelectsTheVisibleDeclaration(t *testing.T) {
	var out bytes.Buffer
	root := completeHelpFixture(&out)
	leaf := root.Command("images").Command("generate")
	inherited := &cli.StringFlag{Name: "header", Aliases: []string{"H"}}
	unrelated := &cli.StringFlag{Name: "remote-only", Usage: "Send a remote option."}
	shadow := &cli.StringFlag{Name: "format", Aliases: []string{"f"}, Usage: "Local image format."}
	leaf.Metadata = map[string]any{"help-content": Content{
		GlobalOptionsNote: "Other commands accept --remote-only.",
		InheritedFlagHelp: map[string]string{"format": "Inherited formats.", "header": "Local header guidance."},
	}}
	root.Flags = append(root.Flags, inherited, unrelated)
	leaf.Flags = append(leaf.Flags, shadow)
	for _, tc := range []struct {
		args []string
		want cli.Flag
	}{
		{[]string{"--help", "--prompt"}, leaf.Flags[1]},
		{[]string{"-h", "-H"}, inherited},
		{[]string{"-h", "--remote-only"}, unrelated},
		{[]string{"--help", "--format"}, shadow},
		{[]string{"--help", "-f"}, shadow},
		{[]string{"sample.png", "--size", "example", "--help", "--format"}, shadow},
	} {
		args := append([]string{"openai", "images", "generate"}, tc.args...)
		normalized, help, err := Configure(root, args)
		if err != nil || !help {
			t.Fatalf("%q: help=%t, error=%v", args, help, err)
		}
		wantArgs := append(slices.Clone(args[:len(args)-2]), "--help")
		if !slices.Equal(normalized, wantArgs) {
			t.Fatalf("%q normalized to %q, want %q", args, normalized, wantArgs)
		}
		if err := root.Run(t.Context(), normalized); err != nil {
			t.Fatal(err)
		}
		if requestedHelpFlag(leaf) != tc.want || requestedHelpFlag(root) != nil {
			t.Fatalf("%q selected the wrong flag or command", args)
		}
		if tc.want == unrelated && (!strings.Contains(out.String(), "openai images generate") || strings.Contains(out.String(), unrelated.Usage)) {
			t.Fatalf("mentioning an unrelated flag in prose selected its reference: %q", out.String())
		}
		if tc.want == inherited && strings.TrimSpace(out.String()) != "Local header guidance." {
			t.Fatalf("inherited alias lost guidance absent from display prose: %q", out.String())
		}
		if tc.want == shadow && !strings.Contains(out.String(), "Local image format.") {
			t.Fatalf("inherited restrictions replaced the local flag description for %q", args)
		}
		out.Reset()
	}
}

func TestOneFlagHelpUsesInstalledVersionAliases(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"--version", "--version print the version"},
		{"-v", "--verbose, -v Local verbose flag."},
	} {
		var out bytes.Buffer
		root := completeHelpFixture(&out)
		root.Version = "synthetic"
		root.Flags = append(root.Flags, &cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}, Usage: "Local verbose flag.", HideDefault: true})
		args, _, err := Configure(root, []string{"openai", "-h", tc.name})
		if err != nil {
			t.Fatal(err)
		}
		if err := root.Run(t.Context(), args); err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(strings.Fields(out.String()), " "); got != tc.want {
			t.Errorf("lookup for %s = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestOneFlagHelpLeavesOtherArgumentsToParser(t *testing.T) {
	var out bytes.Buffer
	root := completeHelpFixture(&out)
	leaf := root.Command("images").Command("generate")
	leaf.Flags = append(leaf.Flags, &cli.StringFlag{Name: "internal", Hidden: true})
	for _, tail := range [][]string{
		{"--help", "--size"}, // Establish a previous selection.
		{"--help"},
		{"--help=true", "--size"},
		{"--help=false", "--size"},
		{"--help", "--size=sample"},
		{"--help", "--size", "sample"},
		{"--help", "-size"},
		{"--help", "---size"},
		{"--help", "--", "--size"},
		{"--prompt", "--help", "--size"},
		{"--help", "--root-only"},
		{"--help", "--internal"},
		{"--help", "--unknown"},
	} {
		args := append([]string{"openai", "images", "generate"}, tail...)
		normalized, _, err := Configure(root, args)
		if err != nil {
			t.Fatal(err)
		}
		if slices.Equal(tail, []string{"--help", "--size"}) {
			if root.Metadata["help-selected-flag"] == nil {
				t.Fatal("missing initial selection")
			}
			continue
		}
		if root.Metadata["help-selected-flag"] != nil {
			t.Fatalf("selected a flag for %q", args)
		}
		if len(tail) > 1 && !slices.Contains(normalized, tail[len(tail)-1]) {
			t.Fatalf("lost final operand: %q => %q", args, normalized)
		}
	}
	leaf.Metadata["local-help-full"] = "authored guide"
	args := []string{"openai", "images", "generate", "--help", "--size"}
	normalized, _, err := Configure(root, args)
	if err != nil || !slices.Equal(normalized, args) || root.Metadata["help-selected-flag"] != nil {
		t.Fatalf("authored guide intercepted lookup: %q, %v", normalized, err)
	}
}

func TestRepeatedHelpDoesNotRetainExpandedFlagDetails(t *testing.T) {
	var out bytes.Buffer
	root := completeHelpFixture(&out)
	leaf := root.Command("images").Command("generate")
	const detail = "Keep the full reference constraint."
	for _, name := range strings.Fields("alpha beta gamma delta epsilon theta eta iota kappa lambda") {
		leaf.Flags = append(leaf.Flags, &cli.BoolFlag{Name: name, Usage: "Inspect a synthetic image option. " + detail})
	}
	for _, test := range []struct {
		args []string
		full bool
	}{
		{[]string{"help", "--all", "images", "generate"}, true},
		{[]string{"images", "generate", "--help"}, false},
		{[]string{"--all=true", "--help", "images", "generate"}, true},
		{[]string{"images", "help", "--all=false", "generate"}, false},
		{[]string{"help", "--all=false", "--all", "images", "generate"}, true},
		{[]string{"--all", "--all=false", "images", "generate", "-h"}, false},
		{[]string{"images", "generate", "--prompt", "--all", "--help"}, false},
	} {
		out.Reset()
		argv := append([]string{"openai"}, test.args...)
		normalized, _, err := Configure(root, argv)
		if err != nil {
			t.Fatal(err)
		}
		if err := root.Run(t.Context(), normalized); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), detail) != test.full || strings.Contains(out.String(), "no longer needed") {
			t.Fatalf("wrong help mode for %q:\n%s", argv, out.String())
		}
	}
}
