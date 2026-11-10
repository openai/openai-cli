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
	layout, _, err := modelLayout(ctx, models, width, style)
	if err != nil {
		return "", true, err
	}
	var content strings.Builder
	content.WriteString(layout)
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

// ConfigureRecordView keeps complete model IDs and owners on narrow screens.
// The fetcher establishes display order before navigation slices the records.
func ConfigureRecordView(opts *listview.Options) {
	ctx, style := opts.Context, opts.Style
	fallback := opts.RenderRecordLabels
	opts.ProjectPrintTable = func(items []gjson.Result) (transformers.ListTableProjection, error) {
		models, supported, err := transformers.ModelRowsInOrder(ctx, items)
		if err != nil || !supported {
			return transformers.ListTableProjection{}, err
		}
		rows, err := modelCells(ctx, models)
		return transformers.ListTableProjection{Headers: []string{"ID", "OWNER"}, Rows: rows, FullValues: true}, err
	}
	opts.RenderTable = func(items []gjson.Result, width int) (string, bool, error) {
		rows, supported, err := transformers.ModelRowsInOrder(ctx, items)
		if err != nil || !supported {
			return "", false, err
		}
		return modelLayout(ctx, rows, width, style)
	}
	opts.RenderRecordLabels = func(item gjson.Result, full bool) (string, error) {
		if !full {
			rows, supported, err := transformers.ModelRowsInOrder(ctx, []gjson.Result{item})
			if err != nil {
				return "", err
			}
			if supported {
				text, _, err := modelLayout(ctx, rows, 0, style)
				return text, err
			}
		}
		return fallback(item, true)
	}
}

func modelCells(ctx context.Context, models []transformers.ModelListRow) ([][]string, error) {
	rows := make([][]string, 0, len(models))
	for _, model := range models {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		owner := "(unknown)"
		if model.Owner != nil {
			owner = *model.Owner
		}
		// Escape formatting controls before choosing a layout or adding labels.
		id := readable.Text(jsonview.SanitizeTerminalString(model.ID))
		owner = readable.Text(jsonview.SanitizeTerminalString(owner))
		rows = append(rows, []string{id, owner})
	}
	return rows, ctx.Err()
}

// modelLayout separates a pinned table from its complete two-field labeled fallback.
func modelLayout(ctx context.Context, models []transformers.ModelListRow, width int, style listview.Style) (string, bool, error) {
	rows, err := modelCells(ctx, models)
	if err != nil {
		return "", false, err
	}
	idWidth, ownerWidth := len("ID"), len("OWNER")
	for _, row := range rows {
		idWidth = max(idWidth, ansi.StringWidth(row[0]))
		ownerWidth = max(ownerWidth, ansi.StringWidth(row[1]))
	}
	var content strings.Builder
	// The shared table caps non-ID columns at 32 cells. Use labels if it would
	// shorten an owner, including the marker for unavailable owner information.
	table, fits := "", false
	if ownerWidth <= 32 && idWidth+2+ownerWidth <= width {
		table, fits = readable.ListTable([]string{"ID", "OWNER"}, rows, width, 0)
	}
	if fits {
		content.WriteString(style.Table(table))
	} else {
		for index, row := range rows {
			if err := ctx.Err(); err != nil {
				return "", false, err
			}
			if index != 0 {
				content.WriteByte('\n')
			}
			// Narrow views retain only these two fields, with complete values.
			fmt.Fprintf(&content, "ID: %s\nOwned by: %s\n", row[0], row[1])
		}
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	return content.String(), fits, nil
}
