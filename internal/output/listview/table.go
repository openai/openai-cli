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
	projection, err := transformers.ProjectListTable(ctx, operation, items)
	if err != nil {
		return "", false, err
	}
	return renderProjection(projection, width, diagnostics, style)
}

// RenderRecordTable presents a caller-verified list, including handwritten lists
// without generated operation metadata. Navigation has its own footer.
func RenderRecordTable(ctx context.Context, operation string, items []gjson.Result, width int, style Style) (string, bool, error) {
	projection, err := transformers.ProjectRecordTable(ctx, operation, items)
	if err != nil {
		return "", false, err
	}
	return renderProjection(projection, width, false, style)
}

func renderProjection(projection transformers.ListTableProjection, width int, diagnostics bool, style Style) (string, bool, error) {
	if len(projection.Headers) == 0 {
		return "", false, nil
	}
	render := readable.ListTable
	if projection.FullValues {
		render = readable.ListTableFullValues
	}
	content, fits := render(projection.Headers, projection.Rows, width)
	if !fits {
		return "", false, nil
	}
	if len(projection.Rows) > 0 {
		content = style.Table(content)
		if diagnostics {
			content += style.Note(ansi.Wrap(clioutput.ResourceSummaryHint, width, "")) + "\n"
		}
	}
	return content, true, nil
}
