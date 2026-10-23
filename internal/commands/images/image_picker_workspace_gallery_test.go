package images

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/openai/openai-cli/internal/imageoutput"
	"github.com/stretchr/testify/require"
)

func TestImagePickerGalleryScrollTraversesEveryImage(t *testing.T) {
	m, w := browserForTest(t, 96, 28)
	for i := range 3 {
		w.appendImage(imagePickerWorkspaceImage{file: imageoutput.SavedImage{Path: fmt.Sprintf("/results/image-%d.png", i)}, rows: strings.Split(strings.Repeat(fmt.Sprintf("pixels-%d\n", i), 24), "\n")})
	}
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	p := &imagePickerInline{model: m, workspace: w}
	w.originKnown = true
	for i := range 3 {
		w.scroll(w.galleryStarts[i] - w.offset)
		require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
		require.Contains(t, strings.Join(w.painted, "\n"), fmt.Sprintf("pixels-%d", i))
	}
	p.updateWorkspace(tea.MouseWheelMsg{Y: w.bodyStart, Button: tea.MouseWheelDown})
	require.Equal(t, 2, w.selected)
	require.Equal(t, 2, w.galleryImageAt(w.offset))
	require.NotContains(t, strings.Join(w.painted, "\n"), "Images ·")
	w.scroll(10000)
	end := w.offset
	w.scroll(3)
	require.Equal(t, end, w.offset)
	require.Equal(t, 2, w.selected, "clamped scrolling must retain its action target")
}

func TestImagePickerGalleryCaptionsFollowPixelsWithoutExtraGap(t *testing.T) {
	m, w := browserForTest(t, 96, 50)
	for i := range 3 {
		w.appendImage(imagePickerWorkspaceImage{file: imageoutput.SavedImage{Path: fmt.Sprintf("/results/image-%d.png", i)}, rows: []string{fmt.Sprintf("pixels-%d", i)}})
	}
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	text := strings.Join(w.displayRows, "\n")
	for i := range 3 {
		require.Equal(t, 1, strings.Count(text, fmt.Sprintf("image-%d.png", i)))
	}
	require.Equal(t, "pixels-0", w.displayRows[w.galleryStarts[1]-1])
	require.Equal(t, len(w.displayRows), w.viewportRows, "small galleries must not pad the remaining terminal height")
}

func TestImagePickerGalleryPreservesAnchorWhenEarlierRowsChange(t *testing.T) {
	m, w := browserForTest(t, 96, 28)
	for range 3 {
		w.appendImage(imagePickerWorkspaceImage{rows: strings.Split(strings.Repeat("pixels\n", 24), "\n")})
	}
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	w.selectImage(1)
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	w.scroll(4)
	relative := w.offset - w.galleryStarts[1]
	w.replaceImageRows(imagePickerExpandedRows{index: 0, rows: []string{"shorter preview"}, columns: 1, protocol: "blocks"})
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	require.Equal(t, relative, w.offset-w.galleryStarts[1])
	require.Equal(t, 1, w.selected)
	w.rows = append(w.rows, "new diagnostic")
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	require.Equal(t, relative, w.offset-w.galleryStarts[1])
}

func TestImagePickerGalleryBackFollowsExplicitExpandedSelection(t *testing.T) {
	m, w := browserForTest(t, 96, 28)
	for range 3 {
		w.appendImage(imagePickerWorkspaceImage{rows: strings.Split(strings.Repeat("pixels\n", 24), "\n")})
	}
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	w.scroll(5)
	before := w.offset
	w.expanded = true
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	require.Equal(t, before, w.offset)
	w.selectImage(1)
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	require.Equal(t, before, w.offset, "expanded browsing must not mutate the hidden gallery")
	p := &imagePickerInline{model: m, workspace: w}
	p.closeExpanded()
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	require.Equal(t, w.galleryStarts[1], w.offset)
	require.Equal(t, 1, w.selected)
}

func TestImagePickerGalleryFindsVisibleOversizedPreview(t *testing.T) {
	m, w := browserForTest(t, 40, 40)
	w.appendImage(imagePickerWorkspaceImage{columns: 16, rows: []string{"fits"}})
	w.appendImage(imagePickerWorkspaceImage{columns: 64, protocol: "font", rows: []string{"cached wide row"}})
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	require.Equal(t, 1, w.inlineResizeIndex(interactiveImageSize{width: m.width, height: m.height}))
	require.NotContains(t, strings.Join(w.painted, "\n"), "cached wide row")
	require.Equal(t, []string{"cached wide row"}, w.images[1].rows)
}

func TestImagePickerGalleryClampsAnchorWhenItsOwnPreviewShrinks(t *testing.T) {
	m, w := browserForTest(t, 96, 28)
	for range 3 {
		w.appendImage(imagePickerWorkspaceImage{rows: strings.Split(strings.Repeat("pixels\n", 40), "\n")})
	}
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	w.scroll(20)
	require.Equal(t, 0, w.selected)
	w.replaceImageRows(imagePickerExpandedRows{index: 0, rows: []string{"smaller preview"}, columns: 1, protocol: "font"})
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	require.Equal(t, 0, w.galleryImageAt(w.offset))
	require.Contains(t, strings.Join(w.painted, "\n"), "smaller preview")
	require.Equal(t, 0, w.selected)
	w.selectImage(1)
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	w.selectImage(-1)
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	require.Equal(t, 0, w.galleryImageAt(w.offset), "restoring an old local offset must remain within that image")
}

func TestImagePickerGalleryRetryRestoresImageAfterDiagnostics(t *testing.T) {
	m, w := browserForTest(t, 96, 28)
	for range 3 {
		w.appendImage(imagePickerWorkspaceImage{rows: strings.Split(strings.Repeat("pixels\n", 30), "\n")})
	}
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	w.selectImage(2)
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	w.scroll(5)
	saved := w.images[2].inlineOffset
	w.rows = []string{"synthetic request failure"}
	w.galleryDetails = true
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	require.Zero(t, w.offset)
	require.Equal(t, 2, w.selected)
	w.clearRequestDiagnostics()
	require.NoError(t, w.draw(m, colorprofile.ASCII, true, true))
	require.Equal(t, 2, w.galleryImageAt(w.offset))
	require.Equal(t, saved, w.offset-w.galleryStarts[2])
}

func TestImagePickerGalleryJumpRevealsAnotherResizeCandidate(t *testing.T) {
	m, w := browserForTest(t, 40, 40)
	w.appendImage(imagePickerWorkspaceImage{columns: 16, rows: strings.Split(strings.Repeat("pixels\n", 50), "\n")})
	w.appendImage(imagePickerWorkspaceImage{columns: 16, rows: []string{"fits"}})
	w.appendImage(imagePickerWorkspaceImage{columns: 64, protocol: "font", rows: []string{"wide native preview"}})
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	w.selectImage(1)
	require.Equal(t, -1, w.inlineResizeIndex(interactiveImageSize{width: m.width, height: m.height}), "the old viewport cannot reveal the next resize candidate")
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	require.Equal(t, 2, w.inlineResizeIndex(interactiveImageSize{width: m.width, height: m.height}), "preparation must inspect the viewport after applying the jump")
}

func TestImagePickerGalleryPagesByItsTinyViewport(t *testing.T) {
	m, w := browserForTest(t, 40, 12)
	for range 3 {
		w.appendImage(imagePickerWorkspaceImage{rows: []string{"pixel one", "pixel two"}})
	}
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	require.Equal(t, 1, w.viewportRows)
	p := &imagePickerInline{model: m, workspace: w}
	p.updateWorkspace(tea.KeyPressMsg{Code: tea.KeyPgDown})
	require.Equal(t, 1, w.offset, "PageDown must not jump across images outside the visible page")
	p.updateWorkspace(tea.KeyPressMsg{Code: tea.KeyPgUp})
	require.Zero(t, w.offset)
}
