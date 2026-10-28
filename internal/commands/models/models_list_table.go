package models

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-cli/internal/output/listview"
	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
)

// RenderTable lists IDs and owners. Explicit machine formats retain complete
// records through their existing presentation paths.
func RenderTable(ctx context.Context, items []gjson.Result, width int, diagnostics bool, style listview.Style) (string, bool, error) {
	return renderTable(ctx, items, width, diagnostics, style, len(items))
}

func renderTable(ctx context.Context, items []gjson.Result, width int, diagnostics bool, style listview.Style, total int) (string, bool, error) {
	options, _ := ctx.Value(modelsListOptionsKey{}).(modelsListOptions)
	models, supported, err := transformers.ProjectModelsList(ctx, items, options.selection.Descending)
	if err != nil || !supported {
		return "", supported, err
	}
	if len(models) == 0 {
		return "No results.\n", true, nil
	}
	rows := make([][]string, 0, len(models))
	idWidth, ownerWidth := len("ID"), len("OWNER")
	for _, model := range models {
		if err := ctx.Err(); err != nil {
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
		content.WriteString(style.Table(table))
	} else {
		for index, row := range rows {
			if err := ctx.Err(); err != nil {
				return "", true, err
			}
			if index != 0 {
				content.WriteByte('\n')
			}
			// Narrow views retain only these two fields, with complete values.
			fmt.Fprintf(&content, "ID: %s\nOwned by: %s\n", row[0], row[1])
		}
	}
	if err := ctx.Err(); err != nil {
		return "", true, err
	}
	if total < 0 {
		noun := "models"
		if len(models) == 1 {
			noun = "model"
		}
		content.WriteString(style.Note(fmt.Sprintf("\nShowing %d %s on this page.\n", len(models), noun)))
	} else if len(models) < total {
		content.WriteString(style.Note(fmt.Sprintf("\nShowing %d of %d models.\n", len(models), total)))
	} else {
		noun := "models"
		if len(models) == 1 {
			noun = "model"
		}
		content.WriteString(style.Note(fmt.Sprintf("\nListed %d %s.\n", len(models), noun)))
	}
	if diagnostics {
		content.WriteString(style.Note(ansi.Wrap("Details: --format json", max(1, width), "")))
		content.WriteByte('\n')
	}
	return content.String(), true, ctx.Err()
}
