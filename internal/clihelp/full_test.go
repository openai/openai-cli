package clihelp

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

func TestCommonFlagRowsPrioritizeRequiredAndExampleFlags(t *testing.T) {
	file := &requestflag.Flag[string]{Name: "file", Required: true, BodyPath: "file", Usage: "Path to the input file. Only WAV and FLAC files are accepted."}
	output := &cli.StringFlag{Name: "output-path", Aliases: []string{"o"}, Usage: "Save the result."}
	command := &cli.Command{Name: "create", Flags: []cli.Flag{
		&cli.IntFlag{Name: "level", Usage: "Choose a level.", Value: 5},
		&cli.IntFlag{Name: "size", Usage: "Image size."},
		&cli.StringFlag{Name: "model", Usage: "Model ID."},
		&cli.StringFlag{Name: "quality", Usage: "Image quality."},
		output, file,
	}}
	for _, width := range []int{24, 40, 80} {
		rows, _ := commonFlagRows(command, "files create -o output.wav", width)
		plain := strings.Join(strings.Fields(rows), " ")
		if !strings.HasPrefix(plain, "--file Path to the input file. --output-path, -o Save the result.") {
			t.Fatalf("width %d: inputs in wrong order or without descriptions: %s", width, rows)
		}
		for _, want := range []string{"--model Model ID.", "--size Image size.", "--quality Image quality."} {
			if !strings.Contains(plain, want) {
				t.Errorf("width %d: missing %q: %s", width, want, rows)
			}
		}
		for _, absent := range []string{"--level", "Only WAV", "Default:"} {
			if strings.Contains(rows, absent) {
				t.Errorf("width %d: unexpected %q: %s", width, absent, rows)
			}
		}
		for _, line := range strings.Split(rows, "\n") {
			if ansi.StringWidth(line) > width {
				t.Errorf("width %d: row too wide: %q", width, line)
			}
		}
	}
	if full := fullFlagGroups(command, command.Flags, 80); !strings.Contains(full, "Only WAV and FLAC") || !strings.Contains(full, "Default: 5") {
		t.Errorf("full flag reference lost details: %s", full)
	}
}

func TestFlagAliasesWrapWithoutSplittingValues(t *testing.T) {
	command := &cli.Command{Writer: &bytes.Buffer{}}
	flag := &cli.StringFlag{Name: "long-header-option", Aliases: []string{"H"}, Usage: "Add a header."}
	got := flagList(command, []cli.Flag{flag}, 35)
	if !strings.Contains(got, "    --long-header-option=TEXT,\n    -H=TEXT\n") {
		t.Fatalf("aliases must wrap as complete labels: %s", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if len(line) > 35 {
			t.Fatalf("flag line exceeds terminal width: %q", line)
		}
	}
	for _, line := range strings.Split(flagList(command, []cli.Flag{flag}, 30), "\n") {
		if len(line) > 30 {
			t.Fatalf("alias separator exceeds terminal width: %q", line)
		}
	}
}

func TestCommonFlagRowsKeepAllRequiredInputs(t *testing.T) {
	command := &cli.Command{Name: "create"}
	for _, name := range []string{"project", "model", "file", "format", "type", "region"} {
		command.Flags = append(command.Flags, &cli.StringFlag{Name: name, Required: true, Usage: "Required input."})
	}
	command.Flags = append(command.Flags, &cli.StringFlag{Name: "extra", Usage: "Additional option."})
	rows, _ := commonFlagRows(command, "create --extra example", 80)
	if strings.Count(rows, "Required input.") != 6 || strings.Contains(rows, "--extra") {
		t.Fatalf("all required inputs must appear before optional flags: %s", rows)
	}
}

func TestFullFlagUsesTypesInsteadOfExamples(t *testing.T) {
	for _, tc := range []struct {
		name, heading, details string
		flag                   cli.Flag
	}{
		{"prompt", "--prompt TEXT", "32000 characters", &requestflag.Flag[string]{
			Name: "prompt", Required: true, BodyPath: "prompt",
			Usage: "A text description, up to 32000 characters; use `dall-e-2` for legacy generation.",
		}},
		{"count", "-n INTEGER", "(default: 1)", &requestflag.Flag[*int64]{
			Name: "n", Default: requestflag.Ptr[int64](1),
			Usage: "Number of images. For `dall-e-3`, only one is supported.",
		}},
		{"nullable boolean", "--stream BOOLEAN", "(default: false)", &requestflag.Flag[*bool]{
			Name: "stream", Default: requestflag.Ptr(false), Usage: "Defaults to `false`.",
		}},
		{"boolean switch", "--debug, -d", "Enable debug mode.", &cli.BoolFlag{
			Name: "debug", Aliases: []string{"d"}, Usage: "Enable `debug` mode.",
		}},
		{"native int", "--limit INTEGER, -l INTEGER", "(default: 12)", &cli.IntFlag{
			Name: "limit", Aliases: []string{"l"}, Value: 12, Usage: "Number of `items`.",
		}},
		{"list", "--tag TEXT, -t TEXT [ --tag TEXT, -t TEXT ]", "(default: \"one\")", &cli.StringSliceFlag{
			Name: "tag", Aliases: []string{"t"}, Value: []string{"one"}, Usage: "Repeat for each `tag`.",
		}},
		{"map", "--field TEXT=TEXT [ --field TEXT=TEXT ]", "Each key has a value.", &cli.StringMapFlag{
			Name: "field", Usage: "Each `key` has a value.",
		}},
		{"environment", "--mode TEXT", "OPENAI_TEST_HELP_MODE", &cli.StringFlag{
			Name: "mode", Value: "auto", Sources: cli.EnvVars("OPENAI_TEST_HELP_MODE"), Usage: "Use `auto` to choose.",
		}},
		{"required native", "--required TEXT", "Required text.", &cli.StringFlag{
			Name: "required", Value: "not-a-default", Required: true, Usage: "Required `text`.",
		}},
		{"default label", "--color TEXT", "(default: automatic)", &cli.StringFlag{
			Name: "color", Value: "auto", DefaultText: "automatic", Usage: "Choose `auto`.",
		}},
		{"credential", "--api-key TEXT", "API key.", &requestflag.Flag[string]{
			Name: "api-key", Default: "fake-help-test-key", HideDefault: true, Usage: "API `key`.",
		}},
		{"unclosed quote", "--input TEXT", "A `text value", &cli.StringFlag{
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
			globalDetails := []string{"GLOBAL FLAGS", "--api-key", "Run $ openai --help for details."}
			if args[len(args)-1] == "--all" {
				globalDetails = []string{"GLOBAL FLAGS", "--api-key=TEXT", "OPENAI_TEST_HELP_KEY"}
			}
			for _, want := range globalDetails {
				if !strings.Contains(got, want) {
					t.Errorf("full help missing %q:\n%s", want, got)
				}
			}
			if strings.Contains(got, "fake-help-test-key") || strings.Contains(got, "Hidden configuration") {
				t.Fatal("hidden information appeared in full help")
			}
			if args[len(args)-1] != "--all" && strings.Contains(got, "OPENAI_TEST_HELP_KEY") {
				t.Fatal("child help repeated the global reference instead of its index")
			}
			if args[len(args)-1] == "images" && (!strings.Contains(got, "--limit=INTEGER") || !strings.Contains(got, "Up to ten images.")) {
				t.Fatal("group help lost its complete local flag description")
			}
			if args[len(args)-1] == "generate" {
				for _, want := range []string{"Image inputs", "--prompt=TEXT", "Complete image reference.", "Use a description with blue and cat.", "All limits stay here."} {
					if !strings.Contains(got, want) {
						t.Errorf("leaf help missing %q:\n%s", want, got)
					}
				}
			}
		})
	}
}

func TestFullHelpGroupingKeepsEachFlagAndItsDetails(t *testing.T) {
	flags := []cli.Flag{
		&cli.StringFlag{Name: "optional", Usage: "A future setting must remain visible."},
		&cli.StringFlag{Name: "prompt", Required: true, Usage: "Required text.\nKeep the important limit."},
		&cli.StringFlag{Name: "image", Aliases: []string{"i"}, Usage: "Path to your file."},
		&cli.IntFlag{Name: "categorized", Category: "Existing category", Usage: "Original category.", Value: 12},
	}
	command := &cli.Command{Name: "edit", Flags: flags, Metadata: map[string]any{
		"help-flag-groups": []FlagGroup{{Title: "Source images", Names: []string{"i"}}},
	}}
	for _, flag := range flags {
		if err := flag.PreParse(); err != nil {
			t.Fatal(err)
		}
	}
	for _, width := range []int{40, 80, 120} {
		got := fullFlagGroups(command, flags, width)
		if strings.Index(got, "Required inputs") > strings.Index(got, "Source images") {
			t.Fatalf("required inputs were not first: %s", got)
		}
		for _, section := range []string{"Required inputs", "Source images", "Other options", "Existing category"} {
			if strings.Count(got, section) != 1 {
				t.Errorf("section %q duplicated or missing: %s", section, got)
			}
		}
		for _, expected := range []struct{ heading, details string }{
			{"--optional=TEXT", "A future setting must remain visible."},
			{"--prompt=TEXT", "Required text. Keep the important limit."},
			{"--image=TEXT, -i=TEXT", "Path to your file."},
			{"--categorized=INTEGER", "Original category. Default: 12"},
		} {
			heading, details := expected.heading, expected.details
			if strings.Count(got, heading) != 1 || !strings.Contains(strings.Join(strings.Fields(got), " "), strings.Join(strings.Fields(details), " ")) {
				t.Errorf("grouping lost flag %q or its details: %s", heading, got)
			}
		}
	}
}

func TestFullHelpAlignsShortFlagEntries(t *testing.T) {
	flags := []cli.Flag{
		&cli.StringFlag{Name: "first", Usage: "First setting."},
		&cli.StringFlag{Name: "second", Usage: "Second setting."},
	}
	got := fullFlagGroups(&cli.Command{}, flags, 80)
	if got != "  --first=TEXT            First setting.\n  --second=TEXT           Second setting.\n" {
		t.Fatalf("short flags must use navigation columns: %q", got)
	}
	if narrow := fullFlagGroups(&cli.Command{}, flags, 40); !strings.Contains(narrow, "First setting.\n\n    --second=TEXT\n") {
		t.Fatalf("narrow flags must keep the stacked reference: %q", narrow)
	}
}

func TestFullHelpStacksTheWholeFlagListWhenDetailsNeedSpace(t *testing.T) {
	command := &cli.Command{}
	for _, flag := range []cli.Flag{
		&cli.StringFlag{Name: "path", Usage: "Read the guide at https://example.invalid/file."},
		&cli.IntFlag{Name: "count", Value: 5},
		&cli.StringFlag{Name: "mode", Sources: cli.EnvVars("OPENAI_TEST_MODE")},
		&cli.StringFlag{Name: "details", Usage: "First paragraph.\n\nSecond paragraph."},
		&cli.StringFlag{Name: "wide", Usage: strings.Repeat("界", 28)},
	} {
		flags := []cli.Flag{&cli.StringFlag{Name: "simple", Usage: "Short option."}, flag}
		if err := flag.PreParse(); err != nil {
			t.Fatal(err)
		}
		got := fullFlagGroups(command, flags, 80)
		if want := flagList(command, flags, 80); got != want {
			t.Errorf("%s: got %q, want the complete reference %q", flag.Names(), got, want)
		}
	}
}

func TestFullHelpUsesOneBlankLineBetweenOptionGroups(t *testing.T) {
	prompt := &cli.StringFlag{Name: "prompt", Required: true, Usage: "Describe the image."}
	quality := &cli.StringFlag{Name: "quality", Usage: "Choose image quality."}
	command := &cli.Command{Name: "generate", Flags: []cli.Flag{prompt, quality}, Metadata: map[string]any{
		"help-flag-groups": []FlagGroup{{Title: "Image settings", Names: []string{"quality"}}},
	}}
	got := commandHelpAtWidth(command, "openai", "images generate", 80)
	for _, want := range []string{
		"FLAGS\n  Required inputs\n    --prompt=TEXT\n",
		"Describe the image.\n\n  Image settings\n    --quality=TEXT\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("option group spacing differs from %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\n\n\n") {
		t.Fatalf("help contains redundant blank lines:\n%s", got)
	}
}

func TestFlagReferenceNormalizesProseWithoutChangingValues(t *testing.T) {
	flag := &cli.StringFlag{
		Name: "path", Value: "`literal` [label](url)",
		Usage: "Select `fav\\.movie`, `data[0]`, or `auto`. Read [the guide](https://example.invalid).",
	}
	if err := flag.PreParse(); err != nil {
		t.Fatal(err)
	}
	before := flag.String()
	got := fullFlagGroups(&cli.Command{}, []cli.Flag{flag}, 100)
	for _, want := range []string{`Select fav\.movie, data[0], or auto.`, "Read the guide (https://example.invalid).", "Default: \"`literal` [label](url)\""} {
		if !strings.Contains(got, want) {
			t.Errorf("flag reference lost %q: %s", want, got)
		}
	}
	if flag.String() != before || flag.Value != "`literal` [label](url)" {
		t.Fatal("presentation changed flag behavior or source metadata")
	}
}

func TestFlagReferenceSeparatesMetadataWithoutAnExtraBlankLine(t *testing.T) {
	const usage = "First paragraph.\n\nCopy this example:\n```sh\nopenai models list\n\nopenai --help\n```\r\n\r\n"
	flags := []cli.Flag{
		&cli.IntFlag{Name: "limit", Value: 4, Usage: usage},
		&cli.StringFlag{Name: "mode", HideDefault: true, Usage: "Choose a mode.\n", Sources: cli.EnvVars("OPENAI_TEST_MODE")},
	}
	got := fullFlagGroups(&cli.Command{}, flags, 80)
	for _, want := range []string{
		"First paragraph.\n\n      Copy this example:",
		"```sh\n      openai models list\n\n      openai --help\n      ```\n      Default: 4\n\n    --mode",
		"Choose a mode.\n      Env: OPENAI_TEST_MODE\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in rendered flags:\n%s", want, got)
		}
	}
}

func TestFlagUsageOverrideRequiresDeclaringOwner(t *testing.T) {
	rootFlag := &cli.StringFlag{Name: "model", Aliases: []string{"m"}, Usage: "Root model."}
	localFlag := &cli.StringFlag{Name: "model", Usage: "", Value: "declared-model"}
	root := &cli.Command{Flags: []cli.Flag{rootFlag}}
	leaf := &cli.Command{Flags: []cli.Flag{localFlag}}
	leaf.Metadata = map[string]any{"help-flag-usages": []FlagUsage{
		{Owner: root, Names: []string{"m"}, Brief: "Choose the root model."},
		{Owner: root, Names: []string{"m"}, Usage: "Root `model` guidance."},
		{Owner: leaf, Names: []string{"model"}, Usage: "ID of the `model` to retrieve."},
	}}
	for _, flag := range []cli.Flag{rootFlag, localFlag} {
		if err := flag.PreParse(); err != nil {
			t.Fatal(err)
		}
	}
	if brief := commonFlagDescription(leaf, rootFlag); brief != "Choose the root model." {
		t.Fatalf("flag alias did not select its owner's brief guidance: %q", brief)
	}
	if brief := commonFlagDescription(leaf, localFlag); brief != "ID of the model to retrieve." {
		t.Fatalf("shadowing flag lost its own guidance: %q", brief)
	}
	if _, full := flagReference(leaf, rootFlag); full != "Root `model` guidance." {
		t.Fatalf("brief override replaced the full reference: %q", full)
	}
	got := fullFlagGroups(leaf, []cli.Flag{localFlag}, 80)
	if !strings.Contains(got, "ID of the model to retrieve.") || strings.Contains(got, "Root model guidance") {
		t.Fatalf("local usage inherited another owner's guidance: %s", got)
	}
	if localFlag.Usage != "" || localFlag.Value != "declared-model" || rootFlag.Usage != "Root model." {
		t.Fatal("usage metadata changed a flag declaration")
	}
	delete(leaf.Metadata, "help-flag-usages")
	if got := fullFlagGroups(leaf, []cli.Flag{localFlag}, 80); strings.Contains(got, "ID of the model") {
		t.Fatalf("usage override persisted after its metadata was removed: %s", got)
	}
}

func TestFlagUsageDescribesOnlyAnUndocumentedRequestModel(t *testing.T) {
	for _, tc := range []struct {
		flag *requestflag.Flag[string]
		want string
	}{
		{&requestflag.Flag[string]{Name: "model", BodyPath: "model"}, pathInputDescription("model")},
		{&requestflag.Flag[string]{Name: "model", BodyPath: "model", Usage: "Choose an audio model.\n"}, "Choose an audio model.\n"},
		{&requestflag.Flag[string]{Name: "model", QueryPath: "model"}, ""},
		{&requestflag.Flag[string]{Name: "engine", Aliases: []string{"model"}, BodyPath: "model"}, ""},
	} {
		if got := commandFlagUsage(&cli.Command{}, tc.flag, tc.flag.Usage); got != tc.want {
			t.Errorf("%s flag usage = %q, want %q", tc.flag.Name, got, tc.want)
		}
	}
}
