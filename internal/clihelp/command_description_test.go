package clihelp

import (
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestCommandDescriptionKeepsOnlyAdditionalDetails(t *testing.T) {
	for _, tc := range []struct {
		name        string
		usage       string
		description string
		want        string
	}{
		{"usage only", "List files.", "", ""},
		{"repeated summary", "List files.", "List files", ""},
		{"next paragraph", "List files.", "List files.\n\nOnly project files appear.", "Only project files appear."},
		{"longer sentence", "List files", "List files created since yesterday.", "List files created since yesterday."},
		{"linked summary", "List [files](https://example.invalid/files).", "", "List files (https://example.invalid/files)."},
		{"literal markup", "List files.", "List files. Pass `**literal**`.", "Pass **literal**."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := &cli.Command{Name: "list", Usage: tc.usage, Description: tc.description}
			help := commandHelpAtWidth(command, "openai", "files list", 79)
			_, description, found := strings.Cut(help, "\nDESCRIPTION\n")
			if tc.want == "" {
				if found {
					t.Fatalf("unexpected DESCRIPTION: %s", description)
				}
				return
			}
			paragraph, _, _ := strings.Cut(description, "\n\n")
			if !found || strings.TrimSpace(paragraph) != tc.want {
				t.Errorf("got description %q, want %q", paragraph, tc.want)
			}
		})
	}
}
