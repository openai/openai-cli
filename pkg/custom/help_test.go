package custom

import (
	"context"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

// Mirror the root flag wrapper's accessor without coupling copy to its type.
type helpRequestFlag struct{ *requestflag.Flag[string] }

func (f *helpRequestFlag) RequestFlag() *requestflag.Flag[string] { return f.Flag }
func (*helpRequestFlag) IsLocal() bool                            { return false }

func TestConfigureHelpHidesConfiguredGlobalValues(t *testing.T) {
	for _, name := range []string{"api-key", "admin-api-key", "webhook-secret", "organization", "project"} {
		for _, order := range []string{"plain", "wrapped-first", "wrapped-after"} {
			t.Run(name+"/"+order, func(t *testing.T) {
				const secret = "fake-help-value-do-not-display"
				flag := &requestflag.Flag[string]{
					Name: name, Default: secret, Sources: cli.EnvVars("OPENAI_TEST_HELP_VALUE"),
				}
				root := &cli.Command{Name: "openai", Flags: []cli.Flag{flag}}
				if order == "wrapped-first" {
					root.Flags[0] = &helpRequestFlag{flag}
				}
				configureGlobalFlagDescriptions(root)
				if order == "wrapped-after" {
					root.Flags[0] = &helpRequestFlag{flag}
				}
				root.Action = func(context.Context, *cli.Command) error { return nil }
				if err := root.Run(context.Background(), []string{"openai", "--" + name, secret}); err != nil {
					t.Fatal(err)
				}
				configureGlobalFlagDescriptions(root)
				help := root.Flags[0].String()
				if strings.Contains(help, secret) || !strings.Contains(help, "OPENAI_TEST_HELP_VALUE") {
					t.Fatalf("help must show the environment name without its value: %q", help)
				}
				if flag.Usage == "" || flag.Default != secret || flag.Get() != secret || !flag.IsSet() {
					t.Fatal("help metadata changed flag values or omitted its description")
				}
			})
		}
	}
}

func TestConfigureHelpPreservesRootAndRequestFlagOwnership(t *testing.T) {
	local := &requestflag.Flag[any]{Name: "project", Usage: "API project membership", BodyPath: "projects"}
	rootProject := &requestflag.Flag[string]{Name: "project"}
	unrelated := &requestflag.Flag[string]{Name: "unrelated", Usage: "Keep this description", Default: "keep-default"}
	child := &cli.Command{Name: "create", Flags: []cli.Flag{local}}
	root := &cli.Command{Name: "openai", Flags: []cli.Flag{rootProject, unrelated}, Commands: []*cli.Command{child}}
	configureGlobalFlagDescriptions(root)
	configureHelpGroups(root)
	if local.Usage != "API project membership" || local.HideDefault || local.BodyPath != "projects" {
		t.Fatal("help metadata changed the request field")
	}
	if unrelated.Usage != "Keep this description" || unrelated.HideDefault || unrelated.Default != "keep-default" {
		t.Fatal("help metadata changed an unrelated root flag")
	}
	groups := child.Metadata["help-flag-groups"].([]clihelp.FlagGroup)
	for i, name := range []string{"Authentication", "Output", "Request options", "Troubleshooting"} {
		if groups[i].Title != name || groups[i].Owner != root {
			t.Fatalf("incorrect root-owned group: %+v", groups[i])
		}
	}
}

func TestConfigureHelpBaseURLUsesLiteralDefault(t *testing.T) {
	const configured = "https://fake-user:fake-password@example.invalid/v1?token=fake-query-token"
	flag := &cli.StringFlag{Name: "base-url", Value: configured, DefaultText: configured}
	root := &cli.Command{Name: "openai", Flags: []cli.Flag{flag}}
	configureGlobalFlagDescriptions(root)
	if got := flag.String(); strings.Contains(got, "fake-") || !strings.Contains(got, "https://api.openai.com/v1") {
		t.Fatalf("base URL help exposed configuration or lost the default: %q", got)
	}
	if flag.Value != configured || !strings.Contains(flag.Usage, "OPENAI_BASE_URL") {
		t.Fatal("help changed the endpoint or lost the environment name")
	}
}

func TestConfigureHelpRefreshesImageExampleInvocation(t *testing.T) {
	images := &cli.Command{Name: "images", Commands: []*cli.Command{
		{Name: "generate"}, {Name: "edit"}, {Name: "create-variation"},
		{Name: "unrelated", Description: "    openai unrelated"},
	}}
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{images}}
	originals := map[string]string{
		"generate": imageGenerationSavingHelp, "edit": imageEditSavingHelp,
		"create-variation": imageVariationSavingHelp,
	}
	// The same tree can be configured again by an embedding caller or a test.
	for _, executable := range []string{"./openai", "./a user's directory/openai", "openai"} {
		args := []string{executable, "help", "--all", "images", "generate"}
		if _, _, err := ConfigureHelp(root, args); err != nil {
			t.Fatal(err)
		}
		invocation := clihelp.Invocation(root.Name, args)
		for name, original := range originals {
			want := strings.Replace(original, "\n    openai ", "\n    "+invocation+" ", 1)
			if got := images.Command(name).Description; got != want {
				t.Errorf("%s example after %q: got %q, want %q", name, executable, got, want)
			}
		}
		if got := images.Command("unrelated").Description; got != "    openai unrelated" {
			t.Fatalf("unrelated description changed: %q", got)
		}
	}
}
