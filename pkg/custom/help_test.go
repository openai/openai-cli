package custom

import (
	"bytes"
	"context"
	"reflect"
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

type helpStringFlag struct{ *cli.StringFlag }

func (f *helpStringFlag) CLIStringFlag() *cli.StringFlag { return f.StringFlag }

func TestConfigureHelpWrappedStringMetadata(t *testing.T) {
	flag := &cli.StringFlag{Name: mtlsClientKeyFileFlag, Value: "synthetic-private-path"}
	root := &cli.Command{Flags: []cli.Flag{&helpStringFlag{flag}}}
	configureGlobalFlagDescriptions(root)
	if !flag.HideDefault || !strings.Contains(flag.Usage, "matching") || strings.Contains(root.Flags[0].String(), flag.Value) {
		t.Fatal("wrapped string flag lost its description or exposed its configured path")
	}
}

func TestConfigureHelpPaginationMetadataPreservesValues(t *testing.T) {
	flag := &requestflag.Flag[int64]{Name: "max-items", Usage: "The maximum number of items to return (use -1 for unlimited)."}
	command := &cli.Command{Name: "list", Flags: []cli.Flag{flag}}
	configureHelpGroups(command)
	configureHelpGroups(command)
	if flag.Default != 0 || flag.IsSet() || flag.DefaultText != "unlimited" || !strings.Contains(flag.Usage, "use 0 for no items") {
		t.Fatal("pagination help changed parser state or lost omitted/zero guidance")
	}
}

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
	labels := child.Metadata["help-flag-labels"].([]clihelp.FlagLabel)
	for _, label := range labels {
		if label.Owner != root {
			t.Fatalf("semantic label could apply to a local request field: %+v", label)
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
	for _, executable := range []string{"./openai", "./a user's directory/openai", "openai"} {
		for name, example := range map[string]string{
			"generate":         `images generate --prompt "A tiny cat" --name cat`,
			"edit":             `images edit --image "photo.png" --prompt "Make the sky purple" --name purple-sky`,
			"create-variation": `images edit --image "photo.png" --prompt "Create a variation of this image" --name variation`,
		} {
			t.Run(executable+"/"+name, func(t *testing.T) {
				var out bytes.Buffer
				root := &cli.Command{Name: "openai", Writer: &out, HideHelpCommand: true,
					Commands: []*cli.Command{{Name: "images", Commands: []*cli.Command{{Name: name}}}},
				}
				// Reconfiguration must not retain the first executable in examples.
				if _, _, err := ConfigureHelp(root, []string{"./old-directory/openai", "help", "images", name}); err != nil {
					t.Fatal(err)
				}
				args := []string{executable, "help", "images", name}
				normalized, _, err := ConfigureHelp(root, args)
				if err != nil {
					t.Fatal(err)
				}
				if err := root.Run(t.Context(), normalized); err != nil {
					t.Fatal(err)
				}
				want := clihelp.Invocation(root.Name, args) + " " + example
				if got := out.String(); strings.Count(got, want) != 1 || strings.Contains(got, "old-directory") {
					t.Fatalf("example is duplicated or retains an old invocation: want %q in %q", want, got)
				}
			})
		}
	}
}

func TestConfigureHelpPreservesImageGuidance(t *testing.T) {
	images := &cli.Command{Name: "images", Commands: []*cli.Command{
		{Name: "generate"}, {Name: "edit"}, {Name: "create-variation"},
		{Name: "unrelated", Description: "Keep this description", CustomHelpTemplate: "Keep this template"},
	}}
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{images}}
	configureImageSaving(root)
	registerImagePreviewCommands(root)
	registerImageModels(root)
	wantGuidance := map[string][]string{
		"generate":         {"interactive terminal", "picker reopens", "Ctrl+C exits", "~/Downloads/gpt-images/", "--format json", "without --name or --output-dir", "depend on the model"},
		"edit":             {"Replace photo.png", "Keeps your original", "~/Downloads/gpt-images/", "existing folder", "--format json", "depend on the model"},
		"create-variation": {"retired and no longer available", "GPT Image model", "Replace photo.png", "legacy variations contract"},
		"preview":          {"PNG, JPEG and WebP", "No API call or key", "original is unchanged", "Resize, then run", "preferences do not affect", "Replace photo.png", "Pipes, CI", "64 MiB", "16 megapixels", "--format auto or text only", "cannot use --transform or --raw-output"},
		"models":           {"exact model names", "individually", "No images are generated", "15 seconds", "no retries", "--offline", "without an API key", "CLI's SDK", "newly released", "permissions or quota", "partial results", "exit nonzero"},
	}
	for _, command := range images.Commands {
		beforeDescription, beforeUsage := command.Description, command.UsageText
		beforeFlags := append([]cli.Flag(nil), command.Flags...)
		configureImageHelpContent(root)
		configureImageHelpContent(root)
		if command.Description != beforeDescription || command.UsageText != beforeUsage || !reflect.DeepEqual(command.Flags, beforeFlags) {
			t.Fatalf("help adaptation changed the runtime definition of %s", command.Name)
		}
		if command.Name == "unrelated" {
			if command.CustomHelpTemplate != "Keep this template" {
				t.Fatal("help adaptation replaced an unrelated feature template")
			}
			continue
		}
		content := command.Metadata["help-content"].(clihelp.Content)
		for _, want := range wantGuidance[command.Name] {
			if !strings.Contains(content.Description, want) {
				t.Errorf("%s help lost %q: %s", command.Name, want, content.Description)
			}
		}
		if command.Name != "models" && len(content.Examples) != 1 {
			t.Errorf("%s help needs one example, got %d", command.Name, len(content.Examples))
		}
	}
	for _, name := range []string{"on", "off"} {
		command := images.Command("inline").Command(name)
		content := command.Metadata["help-content"].(clihelp.Content)
		for _, want := range []string{"future image generation", "No API request or key", "overrides it", "preview ignores", "local Apple Terminal", "--format auto or text only", "cannot use --transform or --raw-output"} {
			if !strings.Contains(content.Description, want) {
				t.Errorf("inline %s help lost %q: %s", name, want, content.Description)
			}
		}
	}
}
