package clihelp

import (
	"bytes"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

func TestFullFlagUsesTypesInsteadOfExamples(t *testing.T) {
	for _, tc := range []struct {
		name, heading, details string
		flag                   cli.Flag
	}{
		{"prompt", "--prompt string", "32000 characters", &requestflag.Flag[string]{
			Name: "prompt", Required: true, BodyPath: "prompt",
			Usage: "A text description, up to 32000 characters; use `dall-e-2` for legacy generation.",
		}},
		{"count", "-n int", "(default: 1)", &requestflag.Flag[*int64]{
			Name: "n", Default: requestflag.Ptr[int64](1),
			Usage: "Number of images. For `dall-e-3`, only one is supported.",
		}},
		{"nullable boolean", "--stream boolean", "(default: false)", &requestflag.Flag[*bool]{
			Name: "stream", Default: requestflag.Ptr(false), Usage: "Defaults to `false`.",
		}},
		{"boolean switch", "--debug, -d", "Enable debug mode.", &cli.BoolFlag{
			Name: "debug", Aliases: []string{"d"}, Usage: "Enable `debug` mode.",
		}},
		{"native int", "--limit int, -l int", "(default: 12)", &cli.IntFlag{
			Name: "limit", Aliases: []string{"l"}, Value: 12, Usage: "Number of `items`.",
		}},
		{"list", "--tag string, -t string [ --tag string, -t string ]", "(default: \"one\")", &cli.StringSliceFlag{
			Name: "tag", Aliases: []string{"t"}, Value: []string{"one"}, Usage: "Repeat for each `tag`.",
		}},
		{"map", "--field string=string [ --field string=string ]", "Each key has a value.", &cli.StringMapFlag{
			Name: "field", Usage: "Each `key` has a value.",
		}},
		{"environment", "--mode string", "OPENAI_TEST_HELP_MODE", &cli.StringFlag{
			Name: "mode", Value: "auto", Sources: cli.EnvVars("OPENAI_TEST_HELP_MODE"), Usage: "Use `auto` to choose.",
		}},
		{"required native", "--required string", "Required text.", &cli.StringFlag{
			Name: "required", Value: "not-a-default", Required: true, Usage: "Required `text`.",
		}},
		{"default label", "--color string", "(default: automatic)", &cli.StringFlag{
			Name: "color", Value: "auto", DefaultText: "automatic", Usage: "Choose `auto`.",
		}},
		{"credential", "--api-key string", "API key.", &requestflag.Flag[string]{
			Name: "api-key", Default: "fake-help-test-key", HideDefault: true, Usage: "API `key`.",
		}},
		{"unclosed quote", "--input string", "A `text value", &cli.StringFlag{
			Name: "input", Usage: "A `text value",
		}},
		{"inverse switch", "--[no-]color, -c", "Use color.", &cli.BoolWithInverseFlag{
			Name: "color", Aliases: []string{"c"}, Usage: "Use `color`.",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.flag.PreParse(); err != nil {
				t.Fatal(err)
			}
			before := tc.flag.String()
			got := fullFlag(tc.flag)
			heading, details, ok := strings.Cut(got, "\t")
			if !ok || heading != tc.heading || !strings.Contains(details, tc.details) {
				t.Fatalf("flag help = %q; want heading %q and details containing %q", got, tc.heading, tc.details)
			}
			_, originalDetails, _ := strings.Cut(before, "\t")
			if details != originalDetails || tc.flag.String() != before {
				t.Fatal("full help changed flag definitions or documentation")
			}
			if strings.Contains(got, "fake-help-test-key") || strings.Contains(got, "not-a-default") {
				t.Fatal("hidden or required-flag defaults appeared in help")
			}
		})
	}
}

func TestFullHelpPreservesCategoriesGlobalOptionsAndDescriptions(t *testing.T) {
	for _, args := range [][]string{
		{"openai", "help", "--all"},
		{"openai", "help", "--all", "images"},
		{"openai", "help", "--all", "images", "generate"},
		{"openai", "images", "help", "--all", "generate"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			var out bytes.Buffer
			input := &cli.StringFlag{Name: "prompt", Usage: "Use a description with `blue` and `cat`.\nAll limits stay here.", Category: "Image inputs"}
			secret := &cli.StringFlag{Name: "api-key", Value: "fake-help-test-key", HideDefault: true, Usage: "The `API` key.", Sources: cli.EnvVars("OPENAI_TEST_HELP_KEY")}
			leaf := &cli.Command{Name: "generate", Description: "Complete image reference.", Flags: []cli.Flag{input}}
			root := &cli.Command{
				Name: "openai", Writer: &out, HideHelpCommand: true,
				Flags:    []cli.Flag{secret, &cli.StringFlag{Name: "hidden", Hidden: true, Usage: "Hidden configuration."}},
				Commands: []*cli.Command{{Name: "images", Flags: []cli.Flag{&cli.IntFlag{Name: "limit", Usage: "Up to `ten` images."}}, Commands: []*cli.Command{leaf}}},
			}
			normalized, help, err := Configure(root, args)
			if err != nil || !help {
				t.Fatalf("Configure = %q, %v, %v", normalized, help, err)
			}
			if err := root.Run(t.Context(), normalized); err != nil {
				t.Fatal(err)
			}
			got := out.String()
			for _, want := range []string{"GLOBAL OPTIONS:", "--api-key string", "OPENAI_TEST_HELP_KEY"} {
				if !strings.Contains(got, want) {
					t.Errorf("full help missing %q:\n%s", want, got)
				}
			}
			if strings.Contains(got, "fake-help-test-key") || strings.Contains(got, "Hidden configuration") {
				t.Fatal("hidden information appeared in full help")
			}
			if args[len(args)-1] == "generate" {
				for _, want := range []string{"Image inputs", "--prompt string", "Complete image reference.", "Use a description with blue and `cat`.", "All limits stay here."} {
					if !strings.Contains(got, want) {
						t.Errorf("leaf help missing %q:\n%s", want, got)
					}
				}
			}
		})
	}
}
