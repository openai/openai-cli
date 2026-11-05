package images

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/require"
)

func TestImagePickerWorkspaceStartsWithOnlyTheForm(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "screen")
	require.NoError(t, err)
	t.Cleanup(func() { out.Close() })
	m := pickerForTest(t)
	m.width, m.height, m.workspace = 96, 40, true
	w := &imagePickerWorkspace{out: out}
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	require.Len(t, w.painted, len(strings.Split(m.View().Content, "\n")), "idle form has no detached status row")
	require.NotContains(t, strings.Join(w.painted, "\n"), "Expand images")
	require.NotContains(t, strings.Join(w.painted, "\n"), "Scroll images")
	_, err = out.Seek(0, io.SeekStart)
	require.NoError(t, err)
	frame, err := io.ReadAll(out)
	require.NoError(t, err)
	require.NotContains(t, string(frame), "\x1b[2J")
	require.NotContains(t, string(frame), "\x1b[?1049")
}

func TestImagePickerWorkspaceExpandedBackPreservesFormAndScroll(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "screen")
	require.NoError(t, err)
	t.Cleanup(func() { out.Close() })
	m := pickerForTest(t)
	m.width, m.height, m.workspace, m.hasImages = 96, 28, true, true
	m.page, m.field, m.focus, m.selected = "choose", "quality", "options", 2
	m.cursor = 5
	w := &imagePickerWorkspace{out: out}
	w.images = []imagePickerWorkspaceImage{{rows: strings.Split(strings.Repeat("immutable preview\n", 40), "\n"), offset: 1, inlineOffset: 7}}
	w.offset = 7
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	before := m.settings
	w.expanded = true
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	p := &imagePickerInline{model: m, workspace: w}
	handled, _ := p.updateWorkspace(tea.PasteMsg{Content: "must not enter hidden prompt"})
	require.True(t, handled)
	p.closeExpanded()
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	require.Equal(t, before, m.settings)
	require.Equal(t, "choose", m.page)
	require.Equal(t, "options", m.focus)
	require.Equal(t, 2, m.selected)
	require.Equal(t, 5, m.cursor)
	require.Equal(t, 7, w.offset)
	require.True(t, time.Now().Before(p.submitAfter), "view return must absorb queued submission keys")
	m.focus = "images"
	p.updateWorkspace(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.False(t, w.expanded, "queued Enter must not reopen the view")
	p.updateWorkspace(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.True(t, w.viewAfter.IsZero(), "explicit navigation clears view-return guard")
	m.focus = "options"
	for _, size := range [][2]int{{20, 4}, {40, 12}, {96, 28}} {
		m.width, m.height = size[0], size[1]
		w.expanded = true
		require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
		require.Contains(t, strings.Join(w.painted, "\n"), "Esc back")
	}
}

func TestImagePickerWorkspaceRejectsStaleExpandedRows(t *testing.T) {
	w := &imagePickerWorkspace{previewRevision: 4, selected: 1}
	p := &imagePickerInline{workspace: w}
	for _, msg := range []imagePickerExpandedRows{{revision: 3, index: 1, rows: []string{"stale revision"}}, {revision: 4, index: 0, rows: []string{"wrong image"}}} {
		handled, cmd := p.updateWorkspace(msg)
		require.True(t, handled)
		require.Nil(t, cmd)
		require.Empty(t, w.expandedRows)
	}
}

func TestImagePickerWorkspaceLoadingKeepsExpandedScroll(t *testing.T) {
	m := pickerForTest(t)
	m.width, m.height = 96, 12
	w := &imagePickerWorkspace{width: 96, height: 12, selected: 0, expandedIndex: 1, images: []imagePickerWorkspaceImage{{rows: []string{"small fallback"}, offset: 7}}}
	_ = w.expandedView(m, false)
	require.Equal(t, 7, w.images[0].offset, "fallback rendering must not consume the retained offset")
	w.expandedIndex = 0
	for range 30 {
		w.expandedRows = append(w.expandedRows, "expanded image row")
	}
	w.scrollExpanded(int(^uint(0) >> 1))
	require.Equal(t, 22, w.images[0].offset)
	w.scrollExpanded(-1)
	require.Equal(t, 21, w.images[0].offset, "Up must work immediately after End")
}

func TestImagePickerWorkspaceMouseUsesReportedPanelOrigin(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "screen")
	require.NoError(t, err)
	t.Cleanup(func() { out.Close() })
	m := pickerForTest(t)
	m.width, m.height, m.workspace = 96, 60, true
	w := &imagePickerWorkspace{out: out, rows: []string{"one image row"}}
	p := &imagePickerInline{model: m, workspace: w, profile: colorprofile.ASCII}
	require.NoError(t, w.draw(m, p.profile, false, true))
	require.False(t, w.originKnown)
	require.True(t, w.originQueryPending)
	p.updateWorkspace(tea.CursorPositionMsg{Y: 10})
	require.True(t, w.originKnown)
	require.Equal(t, 10, w.origin)
	// This absolute row is inside the form, although it resembles a relative image row.
	w.rows = make([]string, 80)
	w.displayRows = make([]string, 80)
	w.offset = 12
	p.updateWorkspace(tea.MouseWheelMsg{Y: w.top + 1, Button: tea.MouseWheelUp})
	require.Equal(t, 12, w.offset)
	p.updateWorkspace(tea.MouseWheelMsg{Y: 10 + w.bodyStart, Button: tea.MouseWheelUp})
	require.Equal(t, 9, w.offset)
}

func TestImagePickerWorkspaceRejectsStaleOriginReport(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "screen")
	require.NoError(t, err)
	t.Cleanup(func() { out.Close() })
	m := pickerForTest(t)
	m.width, m.height, m.workspace = 96, 60, true
	w := &imagePickerWorkspace{out: out}
	p := &imagePickerInline{model: m, workspace: w, profile: colorprofile.ASCII}
	require.NoError(t, w.draw(m, p.profile, false, true))
	initial := w.frameRevision
	m.width = 80
	require.NoError(t, w.draw(m, p.profile, false, true))
	require.Greater(t, w.frameRevision, initial)
	p.updateWorkspace(tea.CursorPositionMsg{Y: 8})
	require.False(t, w.originKnown)
	require.True(t, w.originQueryPending)
	require.Equal(t, w.frameRevision, w.originQueryRevision)
	p.updateWorkspace(tea.CursorPositionMsg{Y: 4})
	require.True(t, w.originKnown)
	require.Equal(t, 4, w.origin)
}

func TestImagePickerWorkspaceExpandedShowsRequestOutcome(t *testing.T) {
	m := pickerForTest(t)
	m.width, m.height = 96, 28
	w := &imagePickerWorkspace{images: []imagePickerWorkspaceImage{{rows: []string{"retained image"}}}}
	for _, note := range []string{"All images saved.", "Request failed. Edit your prompt or settings before trying again."} {
		m.note = note
		lines := w.expandedView(m, false)
		require.Equal(t, "    "+note, lines[2])
		require.Contains(t, lines[3], "retained image")
		require.Contains(t, lines[len(lines)-1], "Esc back")
	}
	require.Contains(t, w.expandedView(m, true)[2], "Generating image")
}
