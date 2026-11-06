package app

import (
	"io"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/output/listview"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
)

// writeProjectDetails lays out the fields selected by OrganizationProjectDetails.
// Fall back before writing any fields if a value needs the full renderer.
func writeProjectDetails(out io.Writer, details gjson.Result, width int, style listview.Style) error {
	labels := map[string]string{
		"name": "Name", "id": "ID", "status": "Status",
		"created_at": "Created", "archived_at": "Archived", "residency": "Residency",
		"external_key_id": "External key ID", "psp_decryption_key_id": "PSP decryption key ID",
	}
	type row struct{ label, value string }
	var rows []row
	labelWidth := 0
	supported := true
	details.ForEach(func(key, field gjson.Result) bool {
		label, known := labels[key.Str]
		if !known || field.Type != gjson.String || field.Str == "" {
			supported = false
			return false
		}
		rows = append(rows, row{label, readable.InlineText(field.Str)})
		labelWidth = max(labelWidth, len(label))
		return true
	})
	if !supported {
		return readable.Write(out, details)
	}

	var text strings.Builder
	for _, r := range rows {
		text.WriteString(style.Label(r.label + ":"))
		if width > 0 && labelWidth+3+ansi.StringWidth(r.value) > width {
			// Give long values their own line so a whole ID can be copied.
			text.WriteByte('\n')
		} else {
			text.WriteString(strings.Repeat(" ", labelWidth-len(r.label)+2))
		}
		text.WriteString(r.value)
		text.WriteByte('\n')
	}
	_, err := io.WriteString(out, text.String())
	return err
}
