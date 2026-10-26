package clihelp

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/commandsuggest"
	"github.com/urfave/cli/v3"
)

type helpSuggestion struct{ shell, text string }
type helpSuggestionRecorder []helpSuggestion

func (r *helpSuggestionRecorder) Record(_ io.Writer, shell, text string) {
	*r = append(*r, helpSuggestion{shell, text})
}

type helpLinkRecorder struct{}

func (*helpLinkRecorder) Record(io.Writer, string, string) {}
func (*helpLinkRecorder) Render(_ io.Writer, shell, text string) string {
	if shell != "" && shell != "zsh" {
		return text
	}
	return "\x1b]8;;openai-insert://Ab01234567/0123456789abcdef0123456789abcdef/1\x1b\\" + text + "\x1b]8;;\x1b\\"
}

func (r *helpLinkRecorder) RenderLabel(output io.Writer, shell, label, _ string) string {
	return r.Render(output, shell, label)
}

func TestSuggestedHelpLinksPreserveVisibleText(t *testing.T) {
	for _, args := range [][]string{
		{"openai", "responses", "create", "--help"},
		{"openai", "help", "setup"},
		{"openai", "help", "responses", "create"},
		{"openai", "--help"},
	} {
		t.Run(strings.Join(args[1:], "-"), func(t *testing.T) {
			var results []string
			for _, links := range []bool{false, true} {
				var output bytes.Buffer
				const example = `responses create --model gpt-5.5 --input "Say hello"`
				leaf := &cli.Command{Name: "create", Metadata: map[string]any{"help-content": Content{Examples: []Example{{Command: example}}}}}
				root := &cli.Command{Name: "openai", Writer: &output, Commands: []*cli.Command{{Name: "responses", Commands: []*cli.Command{leaf}}}}
				if links {
					commandsuggest.SetRecorder(root, &helpLinkRecorder{})
				}
				configured, _, err := Configure(root, args)
				if err != nil {
					t.Fatal(err)
				}
				if err := root.Run(t.Context(), configured); err != nil {
					t.Fatal(err)
				}
				results = append(results, output.String())
			}
			if !strings.Contains(results[1], "\x1b]8;;openai-insert://") || ansi.Strip(results[1]) != results[0] {
				t.Fatalf("links changed help:\n%q\n%q", results[0], results[1])
			}
		})
	}
}

func TestSuggestedCommandsRegisterOnlyRenderedHelp(t *testing.T) {
	var out bytes.Buffer
	leaf := &cli.Command{Name: "create", Usage: "Do not register this prose: openai files delete --file-id unrelated."}
	root := &cli.Command{Name: "openai", Writer: &out, Commands: []*cli.Command{
		{Name: "responses", Commands: []*cli.Command{leaf}},
		{Name: "files", Commands: []*cli.Command{{Name: "create"}}},
	}}
	var recorder helpSuggestionRecorder
	commandsuggest.SetRecorder(root, &recorder)
	args, _, err := Configure(root, []string{"openai", "responses", "create", "--help"})
	if err != nil || len(recorder) != 0 {
		t.Fatalf("configuration registered suggestions: %v, %v", recorder, err)
	}
	// Lazy rendering includes flags added after help configuration.
	leaf.Flags = append(leaf.Flags, &cli.BoolFlag{Name: "added-after-configuration"})
	if err := root.Run(t.Context(), args); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--added-after-configuration") {
		t.Fatalf("lazy rendering omitted a new flag:\n%s", out.String())
	}
	want := helpSuggestionRecorder{
		{"", `openai responses create --model gpt-5.5 --input "Say hello"`},
		{"", "openai help setup"},
	}
	if !reflect.DeepEqual(recorder, want) {
		t.Fatalf("suggestions = %#v; want %#v", recorder, want)
	}
}

func TestSuggestedSetupCommandsKeepShellAnnotations(t *testing.T) {
	var out bytes.Buffer
	root := &cli.Command{Name: "openai", Writer: &out}
	var recorder helpSuggestionRecorder
	commandsuggest.SetRecorder(root, &recorder)
	args, _, err := Configure(root, []string{"openai", "help", "setup"})
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Run(t.Context(), args); err != nil {
		t.Fatal(err)
	}
	for _, want := range []helpSuggestion{
		{"bash", "read -rs OPENAI_API_KEY"},
		{"zsh", "read -rs OPENAI_API_KEY"},
		{"bash", `printf '\n'`},
		{"pwsh", `$openaiKey = Read-Host "API key" -AsSecureString`},
		{"", "openai models list"},
	} {
		found := false
		for _, got := range recorder {
			found = found || got == want
		}
		if !found || !strings.Contains(out.String(), want.text) {
			t.Errorf("missing rendered setup command %#v", want)
		}
	}
}

func TestSuggestedHelpRegistersTypedExamplesWithoutInspectingProse(t *testing.T) {
	var out bytes.Buffer
	const example = "widgets inspect --mode safe"
	leaf := &cli.Command{Name: "inspect", Metadata: map[string]any{"help-content": Content{
		Description: "Never capture this prose: openai widgets delete.",
		Examples:    []Example{{Description: "Inspect one widget.", Command: example}},
	}}}
	root := &cli.Command{Name: "openai", Writer: &out, Commands: []*cli.Command{{Name: "widgets", Commands: []*cli.Command{leaf}},
		{Name: "unseen", Metadata: map[string]any{"help-content": Content{Examples: []Example{{Command: "unseen run"}}}}},
	}}
	var recorder helpSuggestionRecorder
	commandsuggest.SetRecorder(root, &recorder)
	args, _, err := Configure(root, []string{"openai", "help", "widgets", "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Run(t.Context(), args); err != nil {
		t.Fatal(err)
	}
	if want := (helpSuggestionRecorder{{"", "openai " + example}, {"", "openai help setup"}}); !reflect.DeepEqual(recorder, want) {
		t.Fatalf("help captured undeclared or undisplayed commands: %#v", recorder)
	}
	if !strings.Contains(out.String(), "openai widgets delete") || !strings.Contains(out.String(), "openai "+example) {
		t.Fatalf("rendering lost the typed example or ordinary prose: %s", out.String())
	}
}
