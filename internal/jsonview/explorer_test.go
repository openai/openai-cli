package jsonview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/help"
	"github.com/tidwall/gjson"

	"github.com/stretchr/testify/require"
)

func TestNavigateForward_EmptyRowData(t *testing.T) {
	t.Parallel()

	// An empty JSON array produces a TableView with no rows.
	emptyArray := gjson.Parse("[]")
	view, err := newTableView("", emptyArray, false)
	require.NoError(t, err)

	viewer := &JSONViewer{
		stack: []JSONView{view},
		root:  "test",
		help:  help.New(),
	}

	// Should return without panicking despite the empty data set.
	model, cmd := viewer.navigateForward()
	require.Equal(t, model, viewer, "expected same viewer model returned")
	require.Nil(t, cmd)

	// Stack should remain unchanged (no new view pushed).
	require.Equal(t, 1, len(viewer.stack), "expected stack length 1, got %d", len(viewer.stack))
}

// rawJSONItem implements HasRawJSON, returning pre-built JSON.
type rawJSONItem struct {
	raw string
}

func (r rawJSONItem) RawJSON() string { return r.raw }

func TestMarshalItemsToJSONArray_WithHasRawJSON(t *testing.T) {
	t.Parallel()

	items := []any{
		rawJSONItem{raw: `{"id":1,"name":"alice"}`},
		rawJSONItem{raw: `{"id":2,"name":"bob"}`},
	}

	got, err := marshalItemsToJSONArray(items)
	require.NoError(t, err)
	require.JSONEq(t, `[{"id":1,"name":"alice"},{"id":2,"name":"bob"}]`, string(got))
}

func TestSanitizeTerminalStringEscapesControlSequences(t *testing.T) {
	t.Parallel()

	sample := "before\x1b]52;c;ZGF0YQ==\a\u009b31m\nafter\t"
	got := sanitizeTerminalString(sample)

	require.Equal(t, `before\u001b]52;c;ZGF0YQ==\u0007\u009b31m\nafter\t`, got)
	requireNoRawTerminalControls(t, got)
}

func TestStaticDisplayEscapesDecodedStrings(t *testing.T) {
	t.Parallel()

	out := formatJSON(gjson.Parse(`{"\u001b]2;title\u0007":"\u001b]52;c;ZGF0YQ==\u0007"}`), 80)

	require.NotContains(t, out, "\x1b]52")
	require.NotContains(t, out, "\a")
	require.Contains(t, out, `\u001b]2;title\u0007`)
	require.Contains(t, out, `\u001b]52;c;ZGF0YQ==\u0007`)
}

func TestExploreTableEscapesDecodedStringsAndKeys(t *testing.T) {
	t.Parallel()

	view, err := newTableView("", gjson.Parse(`[{"\u001b]2;title\u0007":"\u001b]52;c;sample\u0007"}]`), false)
	require.NoError(t, err)

	columns := view.table.Columns()
	rows := view.table.Rows()

	require.Len(t, columns, 1)
	require.Len(t, rows, 1)
	require.Equal(t, `\u001b]2;title\u0007`, columns[0].Title)
	require.Equal(t, `\u001b]52;c;sample\u0007`, rows[0][0])
	requireNoRawTerminalControls(t, columns[0].Title)
	requireNoRawTerminalControls(t, rows[0][0])
}

func TestExploreObjectPreviewEscapesDecodedStringsAndKeys(t *testing.T) {
	t.Parallel()

	got := formatValue(gjson.Parse(`{"\u001b]2;title\u0007":"\u001b]52;c;sample\u0007"}`), false)

	require.Equal(t, `{\u001b]2;title\u0007:"\u001b]52;c;sample\u0007"}`, got)
	requireNoRawTerminalControls(t, got)
}

func TestExploreTextViewEscapesDecodedString(t *testing.T) {
	t.Parallel()

	view, err := newTextView("", gjson.Parse(`"\u001b]52;c;sample\u0007"`))
	require.NoError(t, err)

	view.Resize(80, 24)
	out := view.View()

	require.Contains(t, out, `\u001b]52;c;sample\u0007`)
	requireNoRawTerminalControls(t, out)
}

func TestExploreSelectedStringEscapesDecodedString(t *testing.T) {
	t.Parallel()

	tableView, err := newTableView("", gjson.Parse(`["\u001b]52;c;sample\u0007"]`), false)
	require.NoError(t, err)

	viewer := &JSONViewer{
		stack: []JSONView{tableView},
		root:  "test",
		help:  help.New(),
	}
	got := viewer.getSelectedContent()

	require.Equal(t, `\u001b]52;c;sample\u0007`, got)
	requireNoRawTerminalControls(t, got)
}

func requireNoRawTerminalControls(t *testing.T, s string) {
	t.Helper()

	require.False(t, strings.ContainsFunc(s, func(r rune) bool {
		if r == '\n' {
			return false
		}
		return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
	}), "expected no raw terminal control characters in %q", s)
}

func TestMarshalItemsToJSONArray_WithoutHasRawJSON(t *testing.T) {
	t.Parallel()

	items := []any{
		map[string]any{"id": 1, "name": "alice"},
		map[string]any{"id": 2, "name": "bob"},
	}

	got, err := marshalItemsToJSONArray(items)
	require.NoError(t, err)
	require.JSONEq(t, `[{"id":1,"name":"alice"},{"id":2,"name":"bob"}]`, string(got))
}
