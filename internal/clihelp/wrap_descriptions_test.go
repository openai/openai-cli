package clihelp

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/urfave/cli/v3"
)

func TestBriefDescriptionsWrapWithoutLosingWords(t *testing.T) {
	description := "Creates an edited or extended image given one or more source images and a prompt."
	command := &cli.Command{Name: "images", Commands: []*cli.Command{{Name: "edit", Usage: description}}}
	for _, width := range []int{32, 40, 80, 120} {
		got := briefHelpAtWidth(command, "openai", "images", width)
		_, listing, _ := strings.Cut(got, "COMMANDS\n")
		listing, _, _ = strings.Cut(listing, "\nCommand help:")
		if !strings.Contains(strings.Join(strings.Fields(listing), " "), description) || strings.Contains(listing, "...") {
			t.Errorf("width %d lost a command description: %s", width, got)
		}
		for _, line := range strings.Split(listing, "\n") {
			if ansi.StringWidth(line) > width {
				t.Errorf("width %d exceeded by %q", width, line)
			}
		}
	}
}

func TestWrapDescriptionsKeepsTokensAndSourceStructure(t *testing.T) {
	source := "Choose gpt-image-2.5-sunburst-2026-09-08 for this request.\n\n  - Keep this constraint.\n    Keep its continuation.\n  {\"name\": \"two  spaces\"}\nWide words: 照片 图片."
	got := wrapDescription(source, "   ", 40)
	for _, want := range []string{"gpt-image-2.5-sunburst-2026-09-08", "\n   \n", "     - Keep this constraint.", "       Keep its continuation.", `{"name": "two  spaces"}`, "照片 图片."} {
		if !strings.Contains(got, want) {
			t.Errorf("wrapped prose lost %q:\n%s", want, got)
		}
	}
}

func TestWrapDescriptionsReflowsSourceLinesButPreservesListsAndCode(t *testing.T) {
	source := "Apple Terminal's auto\nmode uses color blocks.\n\n- A bullet with a\n  continuation.\n- Another bullet.\n\n```sh\nopenai images generate --prompt \"two  words\"\n```"
	got := wrapDescription(source, "   ", 80)
	for _, want := range []string{"Apple Terminal's auto mode uses color blocks.", "- A bullet with a continuation.", "\n   - Another bullet.", "```sh\n   openai images generate --prompt \"two  words\"\n   ```"} {
		if !strings.Contains(got, want) {
			t.Errorf("reflow lost %q:\n%s", want, got)
		}
	}
}

func TestWrapDescriptionsDoesNotInsertNewlinesInCopyableCommands(t *testing.T) {
	command := `openai images edit --image "a file with spaces.png" --prompt "Make the sky purple"`
	for _, source := range []string{command, "    " + command, "```sh\n" + command + "\n```"} {
		got := wrapDescription(source, "   ", 32)
		if !strings.Contains(got, command) {
			t.Errorf("wrapping broke the copyable command: %q", got)
		}
	}
}

func TestConfigureBriefUsesTheRootWriterBeforeParentLinksExist(t *testing.T) {
	var output bytes.Buffer
	leaf := &cli.Command{Name: "edit", Usage: "Creates an edited or extended image given one or more source images and a prompt."}
	group := &cli.Command{Name: "images", Commands: []*cli.Command{leaf}}
	root := &cli.Command{Name: "openai", Writer: &output, Commands: []*cli.Command{group}}
	if _, _, err := Configure(root, []string{"openai", "images", "--help"}); err != nil {
		t.Fatal(err)
	}
	if got, want := group.Metadata["brief-help"], briefHelpAtWidth(group, "openai", "images", 80); got != want {
		t.Fatalf("redirected group help measured another output: %v", got)
	}
}
