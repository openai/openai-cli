package listview

import (
	"fmt"
	"strings"

	clioutput "github.com/openai/openai-cli/internal/output"
	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/tidwall/gjson"
)

// RenderLabels preserves complete values independently of table widths.
func RenderLabels(items []gjson.Result, transform transformers.Transformer, opts clioutput.ReadableOptions) (string, error) {
	var content strings.Builder
	omitted := false
	for i, item := range items {
		if err := opts.Context.Err(); err != nil {
			return "", err
		}
		if i != 0 {
			content.WriteByte('\n')
		}
		item, err := clioutput.Transform(opts.Context, item, transform)
		if err != nil {
			return "", err
		}
		hidden, err := clioutput.WriteReadableResource(&content, item, opts)
		if err != nil {
			return "", err
		}
		omitted = omitted || hidden
	}
	if len(items) == 0 {
		content.WriteString("No results.\n")
	} else if omitted && opts.DiagnosticsAllowed {
		fmt.Fprintln(&content, clioutput.ResourceSummaryHint)
	}
	return content.String(), opts.Context.Err()
}
