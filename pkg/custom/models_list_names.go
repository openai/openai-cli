package custom

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/tidwall/gjson"
)

// The automatic models view lists identities. Explicit formats and printing
// retain the complete records through their existing presentation paths.
func renderModelsListNames(opts ShowJSONOpts, items []gjson.Result, width int) (string, bool, error) {
	options, _ := opts.Context.Value(modelsListOptionsKey{}).(modelsListOptions)
	names, supported, err := transformers.ProjectModelNames(opts.Context, items, options.selection.Descending)
	if err != nil || !supported {
		return "", supported, err
	}
	if len(names) == 0 {
		return "No results.\n", true, nil
	}
	var content strings.Builder
	content.WriteString("ID\n")
	for _, name := range names {
		if err := opts.Context.Err(); err != nil {
			return "", true, err
		}
		// Escape embedded newlines before Text preserves formatting newlines.
		content.WriteString(readable.Text(jsonview.SanitizeTerminalString(name)))
		content.WriteByte('\n')
	}
	noun := "models"
	if len(names) == 1 {
		noun = "model"
	}
	fmt.Fprintf(&content, "\nListed %d %s.\n", len(names), noun)
	content.WriteString(ansi.Wrap("Details: --format json", max(1, width), ""))
	content.WriteByte('\n')
	return content.String(), true, opts.Context.Err()
}

// Models arrive in one response, so printing includes every selected record.
// Keep counts whole when the terminal cannot fit the complete hint.
func modelsListPrintHint(count, width int) string {
	full := fmt.Sprintf("p: print all %d records, quit", count)
	if count == 1 {
		full = "p: print 1 record, quit"
	}
	for _, hint := range []string{
		full,
		fmt.Sprintf("p: all %d, quit", count),
		fmt.Sprintf("p: all %d", count),
	} {
		if len(hint) <= width {
			return hint
		}
	}
	return "p: all"
}
