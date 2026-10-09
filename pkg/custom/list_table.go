package custom

import (
	"context"

	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/tidwall/gjson"
)

// renderListTablePage only presents loaded API items. The caller owns routing,
// automatic-terminal selection, fetching, cancellation, and fallback output.
func renderListTablePage(ctx context.Context, operation string, items []gjson.Result, width int) (string, bool, error) {
	headers, rows, supported, err := transformers.ProjectListTable(ctx, operation, items)
	if err != nil || !supported {
		return "", false, err
	}
	content, fits := readable.ListTable(headers, rows, width)
	if !fits {
		return "", false, nil
	}
	if len(rows) > 0 {
		content += ansi.Wrap(resourceSummaryHint, width, "") + "\n"
	}
	return content, true, nil
}
