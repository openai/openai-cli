package images

import (
	"image"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestImageStudioEditRetainsEvictedNativePlacements(t *testing.T) {
	for _, protocol := range []string{"kitty", "iterm"} {
		t.Run(protocol, func(t *testing.T) {
			m, w := browserForTest(t, 133, 39)
			m.focus, m.draft = "prompt", []rune("old headline\nold subject\nold ending")
			payload := nativeCacheTestRow(protocol, 16, 32)
			rows := make([]string, 12)
			for i := range rows {
				rows[i] = payload
			}
			w.appendImage(imagePickerWorkspaceImage{protocol: protocol, columns: 16, rows: rows})
			require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
			require.Contains(t, nativeCacheOutput(t, w), payload)
			for key := range w.nativeCache {
				w.removeNativeRow(key)
			}
			require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
			idle := nativeCacheOutput(t, w)
			require.NotContains(t, idle, "Preparing preview")
			require.NotContains(t, idle, payload)
			require.NotContains(t, idle, "old headline")

			m.draft = []rune("new headline\nnew subject\nnew ending")
			require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
			update := nativeCacheOutput(t, w)
			found := false
			for y := range w.nativePainted {
				if strings.Contains(w.painted[y], "new ") {
					found = true
				}
			}
			require.True(t, found, "exercise the editor on a physical row that also holds an image")
			g, _ := imageStudioLayout(m.width, m.height)
			require.Contains(t, update, ansi.EraseCharacter(g.previewX))
			require.Contains(t, update, "new subject")
			require.NotContains(t, update, payload)
			require.NotContains(t, update, "Preparing preview")
			require.NotContains(t, update, "\x1b_Ga=d", "editor edits must preserve the installed graphic")
			_, missing := w.missingNativeRow()
			require.False(t, missing)
			requireNativeCacheBound(t, w)
		})
	}
}

func TestImageStudioCleanupUsesPreviousKittyColumn(t *testing.T) {
	m, w := browserForTest(t, 133, 39)
	g, _ := imageStudioLayout(m.width, m.height)
	row := nativeCacheTestRow("kitty", 8, 32)
	w.appendImage(imagePickerWorkspaceImage{protocol: "kitty", columns: 8, rows: []string{row, row}})
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	require.Contains(t, nativeCacheOutput(t, w), row)

	m.width = 109
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	narrow := nativeCacheOutput(t, w)
	require.Contains(t, narrow, imagePickerDeleteGraphics(g.previewX+1))
	require.NotContains(t, narrow, imagePickerDeleteGraphics(5))
	require.Contains(t, narrow, row)

	m.width = 133
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	wide := nativeCacheOutput(t, w)
	require.Contains(t, wide, imagePickerDeleteGraphics(5))
	require.NotContains(t, wide, imagePickerDeleteGraphics(g.previewX+1))
	require.Contains(t, wide, row)
	require.NoError(t, w.close())
	require.Contains(t, nativeCacheOutput(t, w), imagePickerDeleteGraphics(g.previewX+1))
}

func TestImageStudioWheelOnlyScrollsGallery(t *testing.T) {
	m, w := browserForTest(t, 133, 39)
	for range 6 {
		rows := make([]string, 24)
		for i := range rows {
			rows[i] = "sample row"
		}
		w.appendImage(imagePickerWorkspaceImage{rows: rows})
	}
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	w.originKnown, w.origin = true, 0
	p := &imagePickerInline{model: m, workspace: w}
	g, _ := imageStudioLayout(m.width, m.height)
	for _, x := range []int{0, g.previewX - 3, g.previewX + g.previewWidth} {
		p.updateWorkspace(tea.MouseWheelMsg{X: x, Y: w.bodyStart, Button: tea.MouseWheelDown})
		require.Zero(t, w.offset)
	}
	p.updateWorkspace(tea.MouseWheelMsg{X: g.previewX, Y: w.bodyStart, Button: tea.MouseWheelDown})
	require.Equal(t, 3, w.offset)
}

func TestImageStudioPreviewFitsPaneAndCaption(t *testing.T) {
	size := interactiveImageSize{110, 28, 8, 16}
	g, ok := imageStudioLayout(size.width, size.height)
	require.True(t, ok)
	for _, protocol := range []string{"kitty", "iterm", "font", "blocks"} {
		for _, bounds := range []image.Rectangle{image.Rect(0, 0, 1024, 1024), image.Rect(0, 0, 2048, 256), image.Rect(0, 0, 256, 2048)} {
			columns := imagePickerInlinePreviewColumns(bounds, size, protocol)
			require.Positive(t, columns)
			require.LessOrEqual(t, columns, g.previewWidth)
			rows := (columns*size.cellWidth*bounds.Dy() + size.cellHeight*bounds.Dx() - 1) / (size.cellHeight * bounds.Dx())
			require.LessOrEqual(t, rows+1, g.previewRows)
		}
	}
}

func TestImageStudioRoundingDoesNotRepeatPreviewPreparation(t *testing.T) {
	size := interactiveImageSize{110, 28, 8, 16}
	g, ok := imageStudioLayout(size.width, size.height)
	require.True(t, ok)
	// A normalized font placement can exceed its requested height by one row.
	rows := make([]string, g.previewRows)
	w := &imagePickerWorkspace{images: []imagePickerWorkspaceImage{{protocol: "font", rows: rows, columns: 4}}}
	require.Equal(t, 0, w.inlineResizeIndex(size))
	w.replaceImageRows(imagePickerExpandedRows{size: size, rows: rows, columns: 4, protocol: "font"})
	require.Equal(t, -1, w.inlineResizeIndex(size), "a rounded result should scroll, not start another encode")
}
