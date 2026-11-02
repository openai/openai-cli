package images

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/openai/openai-cli/internal/imageoutput"
	"github.com/stretchr/testify/require"
)

func TestImageStudioPointerUsesPaintedPreview(t *testing.T) {
	m, w := browserForTest(t, 120, 30)
	m.focus, m.draft, m.cursor = "prompt", []rune("lake"), 2
	for _, path := range []string{"first.png", "second.png", "third.png"} {
		w.appendImage(imagePickerWorkspaceImage{file: imageoutput.SavedImage{Path: path}, columns: 8, rows: []string{"12345678"}})
	}
	p := &imagePickerInline{workspace: w, model: m}
	paint := func() {
		t.Helper()
		require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
		w.origin, w.originKnown = 4, true
	}
	paint()
	g, _ := imageStudioLayout(m.width, m.height)
	x, y := g.previewX, w.origin+w.bodyStart
	wheel := tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelRight}
	w.originKnown = false
	require.False(t, p.galleryWheel(wheel))
	w.originKnown = true
	require.True(t, p.galleryWheel(wheel))
	require.Equal(t, 1, w.selected)
	require.Equal(t, "prompt", m.focus)
	paint()
	wheel.Button, wheel.Mod = tea.MouseWheelDown, tea.ModShift
	require.True(t, p.galleryWheel(wheel))
	require.Equal(t, 2, w.selected)
	paint()
	click := func(col, row int) bool {
		return p.studioClick(tea.MouseClickMsg{Button: tea.MouseLeft, X: col, Y: row})
	}
	require.False(t, click(x+g.previewWidth-1, y), "disabled next arrow")
	require.False(t, click(x+8, y+1), "blank after the pixels")
	require.False(t, click(x-1, y+1), "divider")
	require.False(t, click(x+g.previewWidth, y+1), "outside the panel")
	require.Equal(t, "prompt", m.focus)
	require.True(t, click(x+g.previewWidth-studioPagerWidth, y))
	require.Equal(t, 1, w.selected)
	require.Equal(t, "images", m.focus)
	paint()
	m.focus = "path"
	require.False(t, click(x, y+1))
	m.focus = "prompt"
	require.True(t, click(x+7, y+1), "last pixel expands")
	require.True(t, w.expanded)
	p.closeExpanded()
	require.Equal(t, "prompt", m.focus)
	require.Equal(t, 2, m.cursor)
	require.Equal(t, "lake", string(m.draft))
}

func TestImageStudioExpandedOpensOnlyVerifiedSavedFile(t *testing.T) {
	m, w := browserForTest(t, 40, 16)
	w.appendImage(imagePickerWorkspaceImage{file: imageoutput.SavedImage{Path: t.TempDir() + "/missing.png"}})
	w.expanded = true
	unexpectedOpen := errors.New("unexpected opener call")
	w.openImage = func(context.Context, string) error { return unexpectedOpen }
	messages := make(chan tea.Msg, 1)
	w.send = func(msg tea.Msg) { messages <- msg }
	p := &imagePickerInline{model: m, workspace: w}
	handled, cmd := p.updateWorkspace(tea.KeyPressMsg{Code: 'o', Text: "o"})
	require.True(t, handled)
	require.Nil(t, cmd)
	select {
	case msg := <-messages:
		opened := msg.(imagePickerOpened)
		require.Error(t, opened.err)
		require.NotErrorIs(t, opened.err, unexpectedOpen)
		p.closeExpanded()
		p.updateWorkspace(opened)
		p.expandImages()
	case <-time.After(time.Second):
		t.Fatal("missing saved-file verification")
	}
	require.Contains(t, strings.Join(w.expandedView(m, false), "\n"), "Could not open this image.")
	require.Contains(t, w.expandedView(m, false)[15], "O open")
}
