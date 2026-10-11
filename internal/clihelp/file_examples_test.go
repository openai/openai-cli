package clihelp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestFilesHelpPreservesDefaultAndSuppliedExamples(t *testing.T) {
	for _, path := range []string{"files create", "files upload"} {
		for _, width := range []int{40, 80} {
			t.Run(fmt.Sprintf("%s/%d", path, width), func(t *testing.T) {
				command := &cli.Command{Name: strings.TrimPrefix(path, "files "), Usage: "Upload a file."}
				fallback := strings.Join(strings.Fields(commandHelpAtWidth(command, "openai", path, width)), " ")
				if !strings.Contains(fallback, "--file ./example.txt") || !strings.Contains(fallback, "example.txt is the path") {
					t.Fatalf("default example or explanation disappeared: %s", fallback)
				}
				command.Metadata = map[string]any{"help-content": Content{Examples: []Example{
					{Description: "Upload an existing file:", Command: `files upload "upload space.txt" --purpose user_data`},
				}}}
				owned := commandHelpAtWidth(command, "openai", path, width)
				if !strings.Contains(owned, `openai files upload "upload space.txt" --purpose user_data`) {
					t.Fatalf("supplied executable example disappeared: %s", owned)
				}
				if strings.Contains(owned, "example.txt") {
					t.Fatalf("default explanation leaked into supplied content: %s", owned)
				}
			})
		}
	}
}
