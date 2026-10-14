package clihelp

import (
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

func TestFlagReferencePreservesDeclaredDefaultsAfterParsing(t *testing.T) {
	for _, tc := range []struct {
		name, supplied, expected string
		flag                     cli.Flag
	}{
		{"native string", "changed", `Default: "auto"`, &cli.StringFlag{Name: "mode", Value: "auto", Usage: "Choose a mode."}},
		{"native boolean", "false", "Default: true", &cli.BoolFlag{Name: "enabled", Value: true, Usage: "Enable the feature."}},
		{"request field", "changed", "Default: original", &requestflag.Flag[string]{Name: "field", Default: "original", Usage: "Request field."}},
		{"required as stdin", "changed", "Default: original", &requestflag.Flag[string]{Name: "field", Default: "original", Required: true, BodyPath: "field", Usage: "Required field or stdin."}},
		{"required native", "changed", "", &cli.StringFlag{Name: "field", Required: true, Value: "original", Usage: "Required flag."}},
		{"unset nullable", "true", "", &requestflag.Flag[*bool]{Name: "enabled", Usage: "Nullable Boolean field."}},
		{"hidden default", "changed", "", &cli.StringFlag{Name: "secret", Value: "private-default", HideDefault: true, Usage: "Secret setting."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.flag.PreParse(); err != nil {
				t.Fatal(err)
			}
			if err := tc.flag.Set(tc.flag.Names()[0], tc.supplied); err != nil {
				t.Fatal(err)
			}
			got := fullFlagGroups(&cli.Command{}, []cli.Flag{tc.flag}, 80)
			if strings.Contains(got, "changed") || strings.Contains(got, "private-default") {
				t.Fatalf("configured value appeared in help: %s", got)
			}
			if tc.expected == "" {
				if strings.Contains(got, "Default:") {
					t.Fatalf("unexpected default: %s", got)
				}
			} else if !strings.Contains(got, "\n      "+tc.expected+"\n") {
				t.Fatalf("missing declared default %q: %s", tc.expected, got)
			}
		})
	}
}

func TestFlagReferenceKeepsMetadataLinesSeparate(t *testing.T) {
	flag := &cli.StringFlag{Name: "mode", Value: "auto", Sources: cli.EnvVars("OPENAI_TEST_MODE"), Usage: "First paragraph.\nEnv: OPENAI_TEST_EXTRA\nThe following prose is independent."}
	got := fullFlagGroups(&cli.Command{}, []cli.Flag{flag}, 80)
	for _, want := range []string{
		"\n      Env: OPENAI_TEST_EXTRA\n      The following prose is independent.\n",
		"\n      Default: \"auto\"\n      Env: OPENAI_TEST_MODE\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("metadata boundary missing %q: %s", want, got)
		}
	}
}

func TestFlagReferenceLabelsRequireDeclaredOwnership(t *testing.T) {
	rootKey := &cli.StringFlag{Name: "api-key", HideDefault: true}
	localKey := &cli.StringFlag{Name: "api-key", HideDefault: true}
	root := &cli.Command{Flags: []cli.Flag{rootKey}}
	command := &cli.Command{Metadata: map[string]any{"help-flag-labels": []FlagLabel{{Owner: root, Names: []string{"api-key"}, Label: "KEY"}}}}
	for _, tc := range []struct {
		flag    cli.Flag
		heading string
	}{{rootKey, "--api-key KEY"}, {localKey, "--api-key TEXT"}} {
		got := fullFlagGroups(command, []cli.Flag{tc.flag}, 80)
		if !strings.Contains(got, tc.heading) {
			t.Fatalf("owner-aware label missing %q: %s", tc.heading, got)
		}
	}
}
