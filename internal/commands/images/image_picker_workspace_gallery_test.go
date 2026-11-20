package images

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
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

func TestImagePickerGalleryFocusAndDuplicateFilenames(t *testing.T) {
	m, w := browserForTest(t, 96, 40)
	m.focus = "prompt"
	w.appendImage(imagePickerWorkspaceImage{file: imageoutput.SavedImage{Path: "/first/bloom.png"}, rows: []string{"first preview"}})
	draw := func() string {
		t.Helper()
		require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
		return ansi.Strip(strings.Join(w.painted, "\n"))
	}
	require.Contains(t, draw(), "│ bloom.png│\n  │ first preview│")
	m.focus = "images"
	require.Contains(t, draw(), "│ bloom.png│", "a single filename has no selection marker")
	w.appendImage(imagePickerWorkspaceImage{file: imageoutput.SavedImage{Path: "/second/bloom.png"}, rows: []string{"second preview"}})
	w.selectImage(1)
	require.Contains(t, draw(), "› bloom.png (2)│\n  │ second preview│")
	require.Contains(t, ansi.Strip(w.expandedView(m, false)[0]), "bloom.png (2)")
	m.focus = "prompt"
	require.Contains(t, draw(), "│ bloom.png (2)│")
	require.NotContains(t, strings.Join(w.painted, "\n"), "› bloom")
}

func TestImagePickerGalleryLongFilenamesKeepDistinctEndings(t *testing.T) {
	m, w := browserForTest(t, 40, 28)
	stem := strings.Repeat("花", 32)
	for _, path := range []string{"/one/" + stem + ".jpeg", "/one/" + stem + "-2.jpeg", "/two/" + stem + "-2.jpeg"} {
		w.appendImage(imagePickerWorkspaceImage{file: imageoutput.SavedImage{Path: path}, protocol: "blocks", rows: []string{"pixels"}})
	}
	w.selectImage(2)
	for _, height := range []int{28, 12} {
		m.height = height
		require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
		text := ansi.Strip(strings.Join(w.painted, "\n"))
		require.Contains(t, text, "-2.jpeg (2)", "height=%d", height)
		heading := w.expandedView(m, false)[0]
		require.Contains(t, ansi.Strip(heading), "-2.jpeg (2)")
		require.LessOrEqual(t, ansi.StringWidth(heading), m.width)
	}
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
	w.addDetail("new diagnostic", imageSecondary)
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
	w.addDetail("synthetic request failure", imageSecondary)
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

// Each wide preview keeps its own scroll position. Neighbors cannot share the pane.
func TestImageStudioCarouselKeepsEachImageAndItsPosition(t *testing.T) {
	m, w := browserForTest(t, 120, 30)
	m.focus = "prompt"
	p := &imagePickerInline{model: m, workspace: w}
	for i, rows := range []int{32, 2, 5} {
		p.updateWorkspace(imagePickerWorkspaceImage{file: imageoutput.SavedImage{Path: fmt.Sprintf("/album/%d.png", i)},
			rows: strings.Split(strings.Repeat(fmt.Sprintf("pixels-%d\n", i), rows), "\n")})
	}
	require.Zero(t, w.selected, "later results must not replace the first image")
	w.scroll(6)
	start := w.offset
	require.Equal(t, 6, start)
	w.selectImage(1)
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	body := strings.Join(w.painted, "\n")
	require.Contains(t, body, "1.png")
	require.Contains(t, body, "pixels-1")
	require.NotRegexp(t, `pixels-[02]|[02]\.png`, body)
	w.scroll(99)
	require.Equal(t, 1, w.selected, "vertical scroll never switches images in the wide pane")
	w.expanded = true
	w.selectImage(-1)
	p.closeExpanded()
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	require.Equal(t, start, w.offset)
	require.Contains(t, strings.Join(w.painted, "\n"), "pixels-0")

	// Opening an older image during generation must survive new arrivals.
	w.requestFirstImage = len(w.images)
	w.selectImage(1)
	p.updateWorkspace(imagePickerWorkspaceImage{rows: []string{"later result"}})
	require.Equal(t, 1, w.selected)
	require.NotContains(t, strings.Join(w.painted, "\n"), "later result")
}
