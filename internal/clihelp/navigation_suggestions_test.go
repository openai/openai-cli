package clihelp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/commandsuggest"
	"github.com/urfave/cli/v3"
)

type navigationRecorder struct {
	commands []string
	targets  []string
}

func (r *navigationRecorder) Record(_ io.Writer, _, target string) {
	r.commands = append(r.commands, target)
}

func (r *navigationRecorder) RenderLabel(output io.Writer, shell, label, target string) string {
	r.Record(output, shell, target)
	r.targets = append(r.targets, target)
	return fmt.Sprintf("\x1b]8;;openai-insert://Ab01234567/0123456789abcdef0123456789abcdef/%d\x1b\\%s\x1b]8;;\x1b\\", len(r.targets), label)
}

func TestNavigationSuggestionsPreserveLayoutAndDiscovery(t *testing.T) {
	for _, width := range []int{52, 80, 100} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			parent := &cli.Command{Name: "widgets", Writer: io.Discard, Commands: []*cli.Command{
				{Name: "delete", Usage: "Keep inspect plain in this description.", Metadata: map[string]any{"help-command-rank": 20}},
				{Name: "nested", Commands: []*cli.Command{{Name: "unseen"}}},
				{Name: "inspect", Usage: "A long description that wraps at narrow widths without changing the command row.", Metadata: map[string]any{"help-command-rank": 10}},
				{Name: "unusually-long-command-name"},
				{Name: "résumé", Usage: "Keep Unicode names aligned."},
				{Name: "hidden", Hidden: true},
				{Name: "legacy:path", Hidden: true, Metadata: map[string]any{"command-compatibility-alias": true}},
			}}
			var recorder navigationRecorder
			commandsuggest.SetRecorder(parent, &recorder)
			plain := commandList(parent, width)
			render := navigationSuggestions(parent, "'/tmp/CLI tools/openai'", "admin widgets")
			if len(recorder.commands) != 0 {
				t.Fatal("preparing help registered suggestions")
			}
			linked := commandList(parent, width, render)
			if ansi.Strip(linked) != plain {
				t.Fatalf("navigation links changed layout:\n%s\nwant:\n%s", linked, plain)
			}
			want := []string{
				"'/tmp/CLI tools/openai' admin widgets inspect --help",
				"'/tmp/CLI tools/openai' admin widgets delete --help",
				"'/tmp/CLI tools/openai' admin widgets unusually-long-command-name --help",
				"'/tmp/CLI tools/openai' admin widgets résumé --help",
				"'/tmp/CLI tools/openai' admin widgets nested --help",
			}
			if !reflect.DeepEqual(recorder.targets, want) {
				t.Fatalf("wrong row order, path, or visibility: %q", recorder.targets)
			}
			if strings.Count(linked, "\x1b]8;;openai-insert://") != len(want) {
				t.Fatal("navigation decorated prose or omitted a row")
			}
		})
	}
}

func TestNavigationSuggestionsFollowNewCommandDefinitions(t *testing.T) {
	for _, upgraded := range []bool{false, true} {
		t.Run(fmt.Sprintf("upgraded=%v", upgraded), func(t *testing.T) {
			var output bytes.Buffer
			actions := 0
			action := func(context.Context, *cli.Command) error { actions++; return nil }
			group := &cli.Command{Name: "widgets", Aliases: []string{"w"}, HideHelpCommand: true, Commands: []*cli.Command{
				{Name: "inspect", Aliases: []string{"i"}, Action: action},
				{Name: "hidden", Hidden: true, Action: action},
			}}
			root := &cli.Command{Name: "openai", Writer: &output, HideHelpCommand: true, Commands: []*cli.Command{group,
				{Name: "unseen", Commands: []*cli.Command{{Name: "delete", Action: action}}},
			}}
			var recorder navigationRecorder
			commandsuggest.SetRecorder(root, &recorder)
			executable := filepath.Join(t.TempDir(), "CLI tools", "openai")
			args, _, err := Configure(root, []string{executable, "w", "--help"})
			if err != nil {
				t.Fatal(err)
			}
			if upgraded {
				// A future feature needs no suggestion table or saved shell catalog.
				group.Commands = append(group.Commands, &cli.Command{Name: "validate", Action: action})
			}
			if len(recorder.commands) != 0 {
				t.Fatal("configuration registered undisplayed commands")
			}
			if err := root.Run(t.Context(), args); err != nil {
				t.Fatal(err)
			}
			invocation := root.Metadata["help-invocation"].(string)
			if !strings.Contains(invocation, "CLI tools") {
				t.Fatalf("help lost the invoked executable: %q", invocation)
			}
			want := []string{invocation + " widgets inspect --help"}
			if upgraded {
				want = append(want, invocation+" widgets validate --help")
			}
			if !reflect.DeepEqual(recorder.targets, want) {
				t.Fatalf("new tree did not supply its displayed rows: %q; want %q", recorder.targets, want)
			}
			if actions != 0 {
				t.Fatalf("help ran %d command actions", actions)
			}
			for _, target := range recorder.commands {
				if strings.Contains(target, " COMMAND ") {
					t.Fatalf("syntax guide became a selectable command: %q", target)
				}
				if strings.Contains(target, "hidden") || strings.Contains(target, "unseen") {
					t.Fatalf("help registered an undisplayed target: %q", target)
				}
			}
		})
	}
}

func TestNavigationSuggestionsRejectUnsafeDefinitionNames(t *testing.T) {
	for _, name := range []string{"widgets;touch-owned", "two words", "$(touch-owned)", "-option", "a|b", "a&b", "a*b", "a?b", "a[b]", "a\\b", "a'b", "a\"b", "a`b", "a\tb", "a\nb"} {
		t.Run(name, func(t *testing.T) {
			parent := &cli.Command{Name: "widgets", Writer: io.Discard, Commands: []*cli.Command{{Name: name}}}
			var recorder navigationRecorder
			commandsuggest.SetRecorder(parent, &recorder)
			plain := commandList(parent, 80)
			got := commandList(parent, 80, navigationSuggestions(parent, "openai", "widgets"))
			if got != plain || len(recorder.commands) != 0 {
				t.Fatalf("unsafe name changed output or became a target: output=%q commands=%q", got, recorder.commands)
			}
		})
	}
}

func TestNavigationSuggestionsCheckActualAncestorNames(t *testing.T) {
	for _, name := range []string{"widgets;touch-owned", "two words", "$(touch-owned)", "-option"} {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			parent := &cli.Command{Name: "safe-parent", HideHelpCommand: true, Commands: []*cli.Command{{Name: "inspect"}}}
			ancestor := &cli.Command{Name: "ancestor", HideHelpCommand: true, Commands: []*cli.Command{parent}}
			root := &cli.Command{Name: "openai", Writer: &output, HideHelpCommand: true, Commands: []*cli.Command{ancestor}}
			args, _, err := Configure(root, []string{"openai", "ancestor", "safe-parent", "--help"})
			if err != nil {
				t.Fatal(err)
			}
			if err := root.Run(t.Context(), args); err != nil {
				t.Fatal(err)
			}
			// The parser has established actual parent links. A safe path string
			// must not bypass validation of a changed ancestor definition.
			ancestor.Name = name
			var recorder navigationRecorder
			commandsuggest.SetRecorder(root, &recorder)
			got := commandList(parent, 80, navigationSuggestions(parent, "openai", "ancestor safe-parent"))
			if got != commandList(parent, 80) || len(recorder.commands) != 0 {
				t.Fatalf("unsafe ancestor supplied a target: %q", recorder.commands)
			}
			// The automatic footer must preserve its plain text without offering
			// the same unsafe ancestor through a second insertion surface.
			parent.Metadata["help-content"] = Content{Examples: []Example{{Command: "models list"}}}
			help := commandHelpAtWidth(parent, "openai", name+" safe-parent", 80)
			if !strings.Contains(help, "Command help: openai "+name+" safe-parent COMMAND --help") {
				t.Fatalf("unsafe ancestor changed visible footer text: %s", help)
			}
			explicitExample := false
			for _, target := range recorder.commands {
				if strings.Contains(target, name) {
					t.Fatalf("unsafe ancestor supplied a footer target: %q", target)
				}
				explicitExample = explicitExample || target == "openai models list"
			}
			if !explicitExample {
				t.Fatal("the navigation gate suppressed an explicitly authored example")
			}
		})
	}
}

func TestNavigationSuggestionsRetainShellSafeDefinitionNames(t *testing.T) {
	for _, name := range []string{"files", "vector-stores", "admin:organization", "@completion", "_private", "version2.1", "résumé", "文字٢"} {
		t.Run(name, func(t *testing.T) {
			parent := &cli.Command{Name: "widgets", Writer: io.Discard, Commands: []*cli.Command{{Name: name}}}
			var recorder navigationRecorder
			commandsuggest.SetRecorder(parent, &recorder)
			got := commandList(parent, 80, navigationSuggestions(parent, "openai", "widgets"))
			want := []string{"openai widgets " + name + " --help"}
			if ansi.Strip(got) != commandList(parent, 80) || !reflect.DeepEqual(recorder.targets, want) {
				t.Fatalf("safe name lost navigation: output=%q targets=%q", got, recorder.targets)
			}
		})
	}
}
