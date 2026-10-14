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

func TestCompleteHelpEntrypointsAndLegacyMigration(t *testing.T) {
	var reference string
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
			legacy := slices.Contains(args, "--all") || slices.Contains(args, "--all=false") ||
				slices.Contains(args, "-all") || slices.Contains(args, "-all=false")
			notice := "Use openai help images generate; --all is no longer needed.\n"
			if legacy {
				if !strings.HasPrefix(got, notice) {
					t.Fatalf("missing migration pointer: %s", got)
				}
				got = strings.TrimPrefix(got, notice)
			}
			if reference == "" {
				reference = got
			}
			if got != reference {
				t.Fatalf("help differs for %q:\n%s\nwant:\n%s", normalized, got, reference)
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

func TestLegacyRootHelpAndSetupKeepCanonicalBody(t *testing.T) {
	for _, path := range [][]string{nil, {"setup"}} {
		var want string
		for _, all := range []bool{false, true} {
			var out bytes.Buffer
			root := completeHelpFixture(&out)
			args := []string{"openai", "help"}
			if all {
				args = append(args, "--all=false")
			}
			args = append(args, path...)
			args, _, err := Configure(root, args)
			if err != nil {
				t.Fatal(err)
			}
			if err := root.Run(t.Context(), args); err != nil {
				t.Fatal(err)
			}
			got := out.String()
			if !all {
				want = got
				continue
			}
			line, body, found := strings.Cut(got, "\n")
			if !found || !strings.Contains(line, "no longer needed") || body != want {
				t.Fatalf("legacy guide differs: %s", got)
			}
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

func TestRepeatedHelpConfigurationClearsLegacyNotice(t *testing.T) {
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
		legacy := slices.Contains(args, "--all") || slices.Contains(args, "-all") || slices.Contains(args, "--all=false")
		if got := strings.Contains(out.String(), "no longer needed"); got != legacy {
			t.Fatalf("migration intent leaked between invocations %q: %s", args, out.String())
		}
	}
}
