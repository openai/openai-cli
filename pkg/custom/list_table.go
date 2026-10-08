package custom

import (
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/tidwall/gjson"
)

// renderListTablePage only presents loaded API items. The caller owns routing,
// automatic-terminal selection, fetching, cancellation, and fallback output.
func renderListTablePage(operation string, items []gjson.Result, width int) (string, bool, error) {
	headers, rows, supported := transformers.ProjectListTable(operation, items)
	if !supported {
		return "", false, nil
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
