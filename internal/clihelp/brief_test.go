package clihelp

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

func TestBriefHelpPreservesCommandAndFlagDefinitions(t *testing.T) {
	model := &requestflag.Flag[string]{
		Name: "model", Aliases: []string{"m"}, Required: true, BodyPath: "model",
		Usage: "Choose the model. This additional documentation must survive full help.",
	}
	quality := &requestflag.Flag[string]{Name: "quality", Default: "auto", BodyPath: "quality", Usage: "Choose quality."}
	modelBefore, qualityBefore := *model, *quality
	command := &cli.Command{Name: "generate", Usage: "Generate an image. Full description.", Flags: []cli.Flag{model, quality}}
	flagsBefore, usageBefore := slices.Clone(command.Flags), command.Usage
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{{Name: "images", Commands: []*cli.Command{command}}}}
	args := []string{"./openai", "images", "generate", "--model", "fake-model", "--quality", "high"}
	got, help, err := Configure(root, args)
	if err != nil || help || !slices.Equal(got, args) {
		t.Fatalf("normal command changed: %q, help=%v, err=%v", got, help, err)
	}
	if !reflect.DeepEqual(*model, modelBefore) || !reflect.DeepEqual(*quality, qualityBefore) {
		t.Error("help changed a flag's aliases, requirement, default, documentation, or request mapping")
	}
	if !slices.Equal(command.Flags, flagsBefore) || command.Usage != usageBefore {
		t.Error("help changed the command's flag order or full description")
	}
	if command.CustomHelpTemplate == "" {
		t.Error("command did not receive compact help")
	}
}

func TestBriefHelpIncludesEveryRequiredInput(t *testing.T) {
	command := &cli.Command{Name: "create", Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "body-input", Required: true, BodyPath: "input", Usage: "Required JSON input."},
		&requestflag.Flag[string]{Name: "path-input", Required: true, PathParam: "id", Usage: "Required path input."},
		&cli.StringFlag{Name: "ordinary-input", Required: true, Usage: "Required CLI input."},
		&requestflag.Flag[string]{Name: "hidden-input", Hidden: true, Required: true},
		&requestflag.Flag[string]{Name: "constant", Const: true, Required: true, Default: "fixed"},
		&cli.StringFlag{Name: "optional-one"},
		&cli.StringFlag{Name: "optional-two"},
		&cli.StringFlag{Name: "optional-three"},
	}}
	body := command.Flags[0].(*requestflag.Flag[string])
	if body.IsRequired() || !body.IsRequiredAsFlagOrStdin() {
		t.Fatal("fixture must exercise input that may be supplied through JSON stdin")
	}
	got := briefHelp(command, "openai", "example create")
	_, inputs, found := strings.Cut(got, "REQUIRED INPUTS")
	if !found {
		t.Fatalf("required inputs are missing: %s", got)
	}
	required, _, _ := strings.Cut(inputs, "OPTIONAL INPUTS")
	for _, name := range []string{"body-input", "path-input", "ordinary-input"} {
		if !strings.Contains(required, "--"+name+" VALUE") {
			t.Errorf("required help lacks --%s: %s", name, got)
		}
	}
	if strings.Contains(got, "hidden-input") || strings.Contains(required, "--constant") {
		t.Errorf("hidden or constant flag was incorrectly presented as required: %s", got)
	}
	if strings.Contains(got, "--optional-three") {
		t.Error("compact help should leave excess optional inputs in full help")
	}
	if !strings.Contains(got, "openai help --all example create") {
		t.Error("compact help must link to the remaining options")
	}
}

func TestBriefHelpGroupsAreBoundedAndHideInternalCommands(t *testing.T) {
	command := &cli.Command{Name: "group", Commands: []*cli.Command{{Name: "internal-only", Hidden: true}}}
	for i := range 12 {
		command.Commands = append(command.Commands, &cli.Command{Name: fmt.Sprintf("command-%02d", i), Usage: "A short description. Extra details."})
	}
	got := briefHelp(command, "./openai", "group")
	for _, name := range []string{"command-00", "command-09", "+ 2 more", "./openai group COMMAND --help", "./openai help --all group"} {
		if !strings.Contains(got, name) {
			t.Errorf("group help lacks %q: %s", name, got)
		}
	}
	for _, name := range []string{"internal-only", "command-10", "Extra details"} {
		if strings.Contains(got, name) {
			t.Errorf("group help should not include %q", name)
		}
	}
}

func TestBriefHelpKeepsExplicitFeatureTemplates(t *testing.T) {
	feature := &cli.Command{Name: "special", CustomHelpTemplate: "Existing feature help\n"}
	hidden := &cli.Command{Name: "internal-only", Hidden: true}
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{feature, hidden}}
	configureCommandHelp(root, "openai", "")
	if feature.CustomHelpTemplate != "Existing feature help\n" || hidden.CustomHelpTemplate != "" {
		t.Error("generic help replaced an explicit template or exposed a hidden command")
	}
}

func TestBriefImageExampleUsesExplicitModelAndCurrentOutput(t *testing.T) {
	got := briefHelp(&cli.Command{Name: "generate"}, "./openai", "images generate")
	for _, text := range []string{`./openai images generate --model `, `--prompt "A tiny orange robot"`, "JSON", "data or a URL"} {
		if !strings.Contains(got, text) {
			t.Errorf("image help lacks %q: %s", text, got)
		}
	}
	for _, text := range []string{"Downloads", "--output-dir", "images preview", "--inline", "default model"} {
		if strings.Contains(got, text) {
			t.Errorf("image help advertises an unshipped behavior: %q", text)
		}
	}
}

func TestGoRunInvocationIsCopyableFromSourceCheckout(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	if err := os.MkdirAll(filepath.Join("cmd", "openai"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("cmd", "openai", "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, executable := range []string{"openai", "openai.exe"} {
		path := filepath.Join(os.TempDir(), "go-build12345", "b001", "exe", executable)
		if got := Invocation("openai", []string{path}); got != "go run ./cmd/openai" {
			t.Errorf("temporary go-run path %q became %q", path, got)
		}
	}
	if got := Invocation("openai", []string{"./openai"}); got != "./openai" {
		t.Errorf("installed/local executable example changed: %q", got)
	}
}

func TestGroupHelpRendererIsScopedToConfiguredRoot(t *testing.T) {
	for _, configured := range []bool{true, false} {
		t.Run(fmt.Sprint(configured), func(t *testing.T) {
			var output bytes.Buffer
			group := &cli.Command{
				Name: "group", CustomHelpTemplate: "SCOPED GROUP TEMPLATE\n",
				Commands: []*cli.Command{{Name: "child", Usage: "Existing child command"}},
			}
			root := &cli.Command{Name: "openai", Writer: &output, Commands: []*cli.Command{group}}
			args := []string{"openai", "group", "--help"}
			if configured {
				var err error
				args, _, err = Configure(root, args)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := root.Run(t.Context(), args); err != nil {
				t.Fatal(err)
			}
			if got := output.String(); configured && got != "SCOPED GROUP TEMPLATE\n" {
				t.Errorf("configured root lost its group template: %q", got)
			} else if !configured && (strings.Contains(got, "SCOPED GROUP TEMPLATE") || !strings.Contains(got, "child")) {
				t.Errorf("unconfigured root's native group renderer changed: %q", got)
			}
		})
	}
}

func TestRepeatedConfigureDoesNotDuplicateCommandsOrRequestSetup(t *testing.T) {
	var setups, actions int
	root := &cli.Command{
		Name: "openai",
		Before: func(ctx context.Context, _ *cli.Command) (context.Context, error) {
			setups++
			return ctx, nil
		},
		Commands: []*cli.Command{{Name: "run", Action: func(context.Context, *cli.Command) error {
			actions++
			return nil
		}}},
	}
	args := []string{"openai", "run"}
	for range 3 {
		var err error
		args, _, err = Configure(root, args)
		if err != nil {
			t.Fatal(err)
		}
	}
	var helpCommands int
	for _, command := range root.Commands {
		if command.Name == "help" {
			helpCommands++
		}
	}
	if helpCommands != 1 {
		t.Fatalf("Configure added %d help commands", helpCommands)
	}
	if err := root.Run(t.Context(), args); err != nil {
		t.Fatal(err)
	}
	if setups != 1 || actions != 1 {
		t.Errorf("normal execution ran request setup %d times and action %d times", setups, actions)
	}
}
