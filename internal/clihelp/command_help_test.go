package clihelp

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

func TestCompleteHelpPreservesCommandAndFlagDefinitions(t *testing.T) {
	model := &requestflag.Flag[string]{Name: "model", Aliases: []string{"m"}, Required: true, BodyPath: "model", Usage: "Choose the model. Keep this complete constraint."}
	quality := &requestflag.Flag[string]{Name: "quality", Default: "auto", BodyPath: "quality", Usage: "Choose quality."}
	modelBefore, qualityBefore := *model, *quality
	command := &cli.Command{Name: "generate", Usage: "Generate an image.", Flags: []cli.Flag{model, quality}}
	flagsBefore := slices.Clone(command.Flags)
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{{Name: "images", Commands: []*cli.Command{command}}}}
	args := []string{"./openai", "images", "generate", "--model", "fake-model", "--quality", "high"}
	got, help, err := Configure(root, args)
	if err != nil || help || !slices.Equal(got, args) {
		t.Fatalf("normal command changed: %q, %v, %v", got, help, err)
	}
	if !reflect.DeepEqual(*model, modelBefore) || !reflect.DeepEqual(*quality, qualityBefore) || !slices.Equal(command.Flags, flagsBefore) {
		t.Fatal("help changed command definitions")
	}
	if command.CustomHelpTemplate == "" {
		t.Fatal("missing complete help")
	}
}

func TestCompleteHelpIncludesEveryInput(t *testing.T) {
	command := &cli.Command{Name: "create", Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "body-input", Required: true, BodyPath: "input", Usage: "Required JSON input. Keep the full constraint."},
		&cli.StringFlag{Name: "ordinary-input", Required: true, Usage: "Required CLI input."},
		&cli.StringFlag{Name: "hidden-input", Hidden: true},
	}}
	for i := range 5 {
		command.Flags = append(command.Flags, &cli.StringFlag{Name: fmt.Sprintf("optional-%d", i), Usage: "Future options remain visible."})
	}
	got := commandHelpAtWidth(command, "openai", "example create", 100)
	for _, want := range []string{"Required inputs", "--body-input TEXT", "--ordinary-input TEXT", "--optional-4 TEXT", "Keep the full constraint."} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
	for _, absent := range []string{"hidden-input", "Full help:", "more in full help"} {
		if strings.Contains(got, absent) {
			t.Errorf("unexpected %q", absent)
		}
	}
}

func TestCompleteHelpGroupsAreCompleteAndHideInternalCommands(t *testing.T) {
	command := &cli.Command{Name: "group", Commands: []*cli.Command{{Name: "internal-only", Hidden: true}}}
	for i := range 12 {
		command.Commands = append(command.Commands, &cli.Command{Name: fmt.Sprintf("command-%02d", i), Usage: "A short description. Extra details."})
	}
	got := commandHelpAtWidth(command, "./openai", "group", 80)
	for _, want := range []string{"command-00", "command-09", "command-10", "command-11", "./openai group COMMAND --help"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, absent := range []string{"internal-only", "+ 2 more", "Extra details"} {
		if strings.Contains(got, absent) {
			t.Errorf("unexpected %q", absent)
		}
	}
}

func TestCompleteHelpKeepsExplicitFeatureTemplates(t *testing.T) {
	feature := &cli.Command{Name: "special", CustomHelpTemplate: "Existing feature help\n"}
	hidden := &cli.Command{Name: "internal-only", Hidden: true}
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{feature, hidden}}
	configureCommandHelp(root, "openai", "")
	if feature.CustomHelpTemplate != "Existing feature help\n" || hidden.CustomHelpTemplate != "" {
		t.Fatal("generic help replaced an explicit template or exposed a hidden command")
	}
}

func TestCompleteHelpExplainsOutputExamples(t *testing.T) {
	command := &cli.Command{Name: "list", Flags: []cli.Flag{&requestflag.Flag[int64]{Name: "max-items", Usage: "Maximum items; use -1 for unlimited."}}}
	for _, width := range []int{40, 80} {
		got := commandHelpAtWidth(command, "'/tmp/CLI build/openai'", "models list", width)
		for _, want := range []string{"EXAMPLES:", "Return complete JSON:", "Print model IDs for a script:", "'/tmp/CLI build/openai' models list --format json", "'/tmp/CLI build/openai' models list --transform id --raw-output", "--max-items INTEGER"} {
			if !strings.Contains(got, want) {
				t.Errorf("width %d missing %q: %s", width, want, got)
			}
		}
	}
}

func TestCompleteHelpContentOverridesExamplesAndDescription(t *testing.T) {
	command := &cli.Command{Name: "generate", Description: "Superseded description.", Metadata: map[string]any{"help-content": Content{
		Description: "Keep the feature's complete caveat.", Examples: []Example{{"Create a synthetic example:", `images generate --prompt "two words"`}},
	}}}
	got := commandHelpAtWidth(command, "'/tmp/my cli/openai'", "images generate", 40)
	for _, want := range []string{"Keep the feature's complete caveat.", `'/tmp/my cli/openai' images generate --prompt "two words"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(got, "Superseded") || strings.Contains(got, "tiny orange robot") {
		t.Fatal("default content duplicated explicit feature guidance")
	}
}

func TestCommandNameKeepsLongUsageInDescription(t *testing.T) {
	command := &cli.Command{Name: "create",
		Usage:       "Create a response. Preserve the complete API restriction.\n\nRead [the guide](https://example.invalid/guide).",
		Description: "Keep the authored caveat.",
	}
	got := commandHelpAtWidth(command, "openai", "responses create", 80)
	name, rest, found := strings.Cut(got, "\nSYNOPSIS:")
	if !found || !strings.Contains(name, "Create a response.") || strings.Contains(name, "restriction") {
		t.Fatalf("NAME is not a short command identity: %s", name)
	}
	_, description, found := strings.Cut(rest, "\nDESCRIPTION:\n")
	if !found {
		t.Fatal("long usage has no DESCRIPTION")
	}
	for _, want := range []string{"Create a response. Preserve the complete API restriction.", "the guide (https://example.invalid/guide)", "Keep the authored caveat."} {
		if !strings.Contains(description, want) {
			t.Errorf("DESCRIPTION lost %q: %s", want, description)
		}
	}
}

func TestAuthoredSummaryPreservesUsageAndContentContract(t *testing.T) {
	command := &cli.Command{Name: "create", Usage: "A long existing usage sentence with important input details.",
		Description: "Superseded description.",
		Metadata: map[string]any{"help-content": Content{
			Summary: "Create an item.", Description: "Keep the complete feature caveat.",
		}},
	}
	got := commandHelpAtWidth(command, "openai", "items create", 100)
	name, _, _ := strings.Cut(got, "\nSYNOPSIS:")
	if !strings.Contains(name, "Create an item.") || strings.Contains(name, "existing usage") {
		t.Fatalf("authored summary was not used: %s", name)
	}
	if !strings.Contains(got, command.Usage) || !strings.Contains(got, "Keep the complete feature caveat.") || strings.Contains(got, "Superseded") {
		t.Fatalf("description content changed: %s", got)
	}
}

func TestInputNoteOverridesOnlySynopsisGuidance(t *testing.T) {
	prompt := &requestflag.Flag[string]{Name: "prompt", Required: true, BodyPath: "prompt"}
	before := *prompt
	var parsed string
	command := &cli.Command{Name: "generate", Flags: []cli.Flag{prompt},
		Metadata: map[string]any{"help-content": Content{InputNote: "Choose a prompt interactively or supply it through flags or piped JSON/YAML."}},
		Action: func(_ context.Context, command *cli.Command) error {
			parsed = command.String("prompt")
			return nil
		},
	}
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{command}}
	args := []string{"openai", "generate", "--prompt", "synthetic prompt"}
	normalized, help, err := Configure(root, args)
	if err != nil || help || !slices.Equal(normalized, args) {
		t.Fatalf("authored note changed normal routing: %q, %v, %v", normalized, help, err)
	}
	got := commandHelpAtWidth(command, "openai", "generate", 100)
	if !strings.Contains(got, "Choose a prompt interactively") || strings.Contains(got, "Required request inputs:") {
		t.Fatalf("authored input note did not replace automatic guidance: %s", got)
	}
	if !reflect.DeepEqual(*prompt, before) || command.Flags[0] != prompt {
		t.Fatal("authored note changed a flag declaration")
	}
	if err := root.Run(t.Context(), normalized); err != nil {
		t.Fatal(err)
	}
	if parsed != "synthetic prompt" {
		t.Fatalf("normal parser received %q", parsed)
	}
}

func TestInheritedGlobalIndexKeepsEveryNameAndRootReference(t *testing.T) {
	var out bytes.Buffer
	leaf := &cli.Command{Name: "run", Flags: []cli.Flag{&cli.StringFlag{Name: "project", Usage: "Local project field."}},
		Metadata: map[string]any{"help-content": Content{GlobalOptionsNote: "This command supports text output only."}},
	}
	root := &cli.Command{Name: "openai", Writer: &out, Commands: []*cli.Command{leaf}, Flags: []cli.Flag{
		&cli.StringFlag{Name: "root-option", Aliases: []string{"r"}, Usage: "Complete inherited explanation."},
		&cli.StringFlag{Name: "project", Usage: "Root project explanation."},
		&cli.BoolFlag{Name: "hidden-global", Hidden: true},
		&cli.BoolFlag{Name: "root-only", Local: true},
	}}
	for i := range 12 {
		root.Flags = append(root.Flags, &cli.BoolFlag{Name: fmt.Sprintf("future-%02d", i), Usage: "Keep future flags visible."})
	}
	args, _, err := Configure(root, []string{"/tmp/CLI build/openai", "run", "--help"})
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Run(t.Context(), args); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	_, index, found := strings.Cut(got, "\nGLOBAL OPTIONS:\n")
	if !found {
		t.Fatal("missing inherited flag index")
	}
	rootReference := "Global option details: '/tmp/CLI build/openai' --help"
	if runtime.GOOS == "windows" {
		rootReference = "Global option details: & '/tmp/CLI build/openai' --help"
	}
	for _, want := range []string{"--root-option, -r", "--future-00", "--future-11", rootReference, "This command supports text output only."} {
		if !strings.Contains(index, want) {
			t.Errorf("global index lost %q: %s", want, index)
		}
	}
	for _, absent := range []string{"Complete inherited explanation", "Root project explanation", "--project", "--hidden-global", "--root-only"} {
		if strings.Contains(index, absent) {
			t.Errorf("global index contains %q: %s", absent, index)
		}
	}
	if !strings.Contains(got, "Local project field.") {
		t.Fatal("local flag description was removed")
	}
	rootHelp := commandHelpAtWidth(root, "openai", "", 80)
	if !strings.Contains(rootHelp, "Complete inherited explanation.") || !strings.Contains(rootHelp, "Root project explanation.") {
		t.Fatal("root help lost full global descriptions")
	}
}

func TestGoRunInvocationIsCopyableFromSourceCheckout(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	cacheKey := strings.Repeat("a1", 32)
	var paths []string
	for _, executable := range []string{"openai", "openai.exe"} {
		paths = append(paths,
			filepath.Join(os.TempDir(), "go-build12345", "b001", "exe", executable),
			filepath.Join(os.TempDir(), "custom cache", "a1", cacheKey+"-d", executable),
		)
	}
	for _, path := range paths {
		if got := Invocation("openai", []string{path}); got != "openai" {
			t.Errorf("go-run path outside a checkout %q became %q", path, got)
		}
	}
	if err := os.MkdirAll(filepath.Join("cmd", "openai"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("cmd", "openai", "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if got := Invocation("openai", []string{path}); got != "go run ./cmd/openai" {
			t.Errorf("go-run path inside a checkout %q became %q", path, got)
		}
	}
	for _, path := range []string{
		"./openai",
		filepath.Join("go-build-tools", "openai"),
		filepath.Join("cache", "a1", "not-a-digest-d", "openai"),
		filepath.Join("cache", "a2", cacheKey+"-d", "openai"),
		filepath.Join("cache", "z1", strings.Repeat("z1", 32)+"-d", "openai"),
	} {
		if got := Invocation("openai", []string{path}); got != path {
			t.Errorf("installed/local executable %q changed to %q", path, got)
		}
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
