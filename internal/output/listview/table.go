package listview

import (
	"context"

	"github.com/charmbracelet/x/ansi"
	clioutput "github.com/openai/openai-cli/internal/output"
	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
)

// RenderTable only presents loaded API items. The caller owns routing,
// automatic-terminal selection, fetching, cancellation, and fallback output.
func RenderTable(ctx context.Context, operation string, items []gjson.Result, width int, diagnostics bool, style Style) (string, bool, error) {
	headers, rows, supported, err := transformers.ProjectListTable(ctx, operation, items)
	if err != nil || !supported {
		return "", false, err
	}
	content, fits := readable.ListTable(headers, rows, width)
	if !fits {
		return "", false, nil
	}
	if len(rows) > 0 {
		content = style.Table(content)
		if diagnostics {
			content += style.Note(ansi.Wrap(clioutput.ResourceSummaryHint, width, "")) + "\n"
		}
	}
	return content, true, nil
}
