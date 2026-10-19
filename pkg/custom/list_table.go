package custom

import (
	"context"

	"github.com/charmbracelet/x/ansi"
	clioutput "github.com/openai/openai-cli/internal/output"
	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/openai/openai-cli/internal/readable"
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
	if len(rows) > 0 && outputDiagnosticsAllowed(ctx) {
		content += ansi.Wrap(clioutput.ResourceSummaryHint, width, "") + "\n"
	}
	return content, true, nil
}
