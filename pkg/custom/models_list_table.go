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

// The automatic models view lists IDs and owners. Explicit formats and printing
// retain the complete records through their existing presentation paths.
func renderModelsListTable(opts ShowJSONOpts, items []gjson.Result, width int) (string, bool, error) {
	options, _ := opts.Context.Value(modelsListOptionsKey{}).(modelsListOptions)
	models, supported, err := transformers.ProjectModelsList(opts.Context, items, options.selection.Descending)
	if err != nil || !supported {
		return "", supported, err
	}
	if len(models) == 0 {
		return "No results.\n", true, nil
	}
	rows := make([][]string, 0, len(models))
	idWidth, ownerWidth := len("ID"), len("OWNER")
	for _, model := range models {
		if err := opts.Context.Err(); err != nil {
			return "", true, err
		}
		owner := "(unknown)"
		if model.Owner != nil {
			owner = *model.Owner
		}
		// Escape formatting controls before choosing a layout or adding labels.
		id := readable.Text(jsonview.SanitizeTerminalString(model.ID))
		owner = readable.Text(jsonview.SanitizeTerminalString(owner))
		rows = append(rows, []string{id, owner})
		idWidth = max(idWidth, ansi.StringWidth(id))
		ownerWidth = max(ownerWidth, ansi.StringWidth(owner))
	}
	var content strings.Builder
	// The shared table caps non-ID columns at 32 cells. Use labels if it would
	// shorten an owner, including the marker for unavailable owner information.
	table, fits := "", false
	if ownerWidth <= 32 && idWidth+2+ownerWidth <= width {
		table, fits = readable.ListTable([]string{"ID", "OWNER"}, rows, width)
	}
	if fits {
		content.WriteString(table)
	} else {
		for index, row := range rows {
			if err := opts.Context.Err(); err != nil {
				return "", true, err
			}
			if index != 0 {
				content.WriteByte('\n')
			}
			// Narrow views retain only these two fields, with complete values.
			fmt.Fprintf(&content, "ID: %s\nOwned by: %s\n", row[0], row[1])
		}
	}
	if err := opts.Context.Err(); err != nil {
		return "", true, err
	}
	noun := "models"
	if len(models) == 1 {
		noun = "model"
	}
	fmt.Fprintf(&content, "\nListed %d %s.\n", len(models), noun)
	if outputDiagnosticsAllowed(opts.Context) {
		content.WriteString(ansi.Wrap("Details: --format json", max(1, width), ""))
		content.WriteByte('\n')
	}
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
