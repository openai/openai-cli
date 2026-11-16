package listview

import (
	"context"
	"os"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	clioutput "github.com/openai/openai-cli/internal/output"
	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
)

// TerminalTableRows counts one-line records in a full screen with a footer.
// A collapsed pane loads one record and waits until it can be shown.
func TerminalTableRows(out *os.File, hint string) (int, error) {
	width, height, err := term.GetSize(out.Fd())
	if err != nil {
		return 0, err
	}
	if width <= 1 {
		return 1, nil
	}
	return tableDataRows(recordBodyHeight(width, height, hint)), nil
}

func recordHintFits(width, height int, hint string) bool {
	return hint != "" && height >= 4 && ansi.StringWidth(hint) <= max(1, width-1)
}

func recordBodyHeight(width, height int, hint string) int {
	if recordHintFits(width, height, hint) {
		return height - 2
	}
	return height - 1
}

// Pin the heading only when a data row also fits.
func tableDataRows(bodyHeight int) int { return max(1, bodyHeight-1) }

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
	var content string
	var fits bool
	if projection.FullValues {
		content, fits = readable.ListTableFullValues(projection.Headers, projection.Rows, width)
	} else {
		content, fits = readable.ListTable(projection.Headers, projection.Rows, width, projection.ProtectedColumn)
	}
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
