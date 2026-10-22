package images

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/imageoutput"
	"github.com/stretchr/testify/require"
)

func browserForTest(t *testing.T, width, height int) (*imagePicker, *imagePickerWorkspace) {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "screen")
	require.NoError(t, err)
	t.Cleanup(func() { out.Close() })
	m := pickerForTest(t)
	m.width, m.height, m.workspace, m.hasImages, m.focus = width, height, true, true, "images"
	return m, &imagePickerWorkspace{out: out}
}

func TestImagePickerWorkspaceSelectedImageMatchesItsCaption(t *testing.T) {
	m, w := browserForTest(t, 96, 40)
	for i := 0; i < 4; i++ {
		w.appendImage(imagePickerWorkspaceImage{file: imageoutput.SavedImage{Path: fmt.Sprintf("/synthetic/results/image-%d.png", i)}, rows: []string{fmt.Sprintf("pixels-%d", i)}})
	}
	w.selected = 2
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	text := ansi.Strip(strings.Join(w.painted, "\n"))
	require.NotContains(t, text, "3 of 4")
	require.Contains(t, text, "Images")
	require.Equal(t, 1, strings.Count(text, "image-2.png"))
	require.Contains(t, text, "Saved in results")
	require.NotContains(t, text, "/synthetic/")
	require.Contains(t, text, "pixels-2")
	require.Contains(t, text, "pixels-0")
	require.Contains(t, text, "pixels-1")
	require.Contains(t, text, "pixels-3")
	require.Contains(t, text, "› Image 3 · image-2.png")
	require.Less(t, strings.Index(text, "Ctrl+C"), strings.Index(text, "pixels-2"))
	require.NotContains(t, text, "[Ctrl+P]")
	require.NotContains(t, text, "[Enter]")
}

func TestImagePickerWorkspaceRetainsEachImagesScrollAcrossViews(t *testing.T) {
	m, w := browserForTest(t, 96, 28)
	for i := 0; i < 2; i++ {
		w.appendImage(imagePickerWorkspaceImage{rows: strings.Split(strings.Repeat("pixels\n", 50), "\n"), offset: 19})
	}
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	w.scroll(7)
	w.selectImage(1)
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	require.Equal(t, w.galleryStarts[1], w.offset)
	w.scroll(3)
	w.selectImage(-1)
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	require.Equal(t, 7, w.offset)
	w.expanded = true
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	w.scrollExpanded(-1)
	p := &imagePickerInline{model: m, workspace: w}
	p.closeExpanded()
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	require.Equal(t, 7, w.offset)
	require.Equal(t, 18, w.images[0].offset)
	w.selectImage(1)
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	require.Equal(t, w.galleryStarts[1]+3, w.offset)
}

func TestImagePickerWorkspaceArrivalDoesNotReplaceBrowsedImage(t *testing.T) {
	m, w := browserForTest(t, 96, 28)
	w.appendImage(imagePickerWorkspaceImage{rows: strings.Split(strings.Repeat("first pixels\n", 50), "\n")})
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	p := &imagePickerInline{model: m, workspace: w}
	p.updateWorkspace(imagePickerWorkspaceImage{rows: []string{"second pixels"}})
	require.Equal(t, 0, w.selected)
	require.Contains(t, strings.Join(w.painted, "\n"), "first pixels")
	require.NotContains(t, strings.Join(w.painted, "\n"), "second pixels")
}

func TestImagePickerWorkspaceKeepsReceiptsOutsideTheActivePanel(t *testing.T) {
	m, w := browserForTest(t, 96, 28)
	w.send = func(msg tea.Msg) {
		if entry, ok := msg.(imagePickerWorkspaceImage); ok {
			w.appendImage(entry)
		}
	}
	files := []imageoutput.SavedImage{{Path: "/synthetic/results/first.png"}, {Path: "/synthetic/results/second.png"}}
	require.NoError(t, w.saved(context.Background(), files, "off"))
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	require.NotContains(t, strings.Join(w.painted, "\n"), "Saved image:")
	require.NotContains(t, strings.Join(w.painted, "\n"), "/synthetic/")
	require.NoError(t, w.close())
	require.NoError(t, w.close())
	_, err := w.out.Seek(0, io.SeekStart)
	require.NoError(t, err)
	raw, err := io.ReadAll(w.out)
	require.NoError(t, err)
	for _, f := range files {
		require.Equal(t, 1, strings.Count(string(raw), fmt.Sprintf("Saved image: %+q", f.Path)))
	}
}

func TestImagePickerWorkspaceFooterStaysOutsideTheScrollRegion(t *testing.T) {
	for _, width := range []int{40, 80, 96} {
		m, w := browserForTest(t, width, 28)
		w.appendImage(imagePickerWorkspaceImage{rows: strings.Split(strings.Repeat("pixels\n", 80), "\n")})
		require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
		footerStart, _ := pickerNavigationLine(t, strings.Join(w.painted, "\n"), "Ctrl+C")
		w.originKnown = true
		p := &imagePickerInline{model: m, workspace: w}
		w.scroll(7)
		p.updateWorkspace(tea.MouseWheelMsg{Y: footerStart, Button: tea.MouseWheelDown})
		require.Equal(t, 7, w.offset, "the footer must not scroll results")
		p.updateWorkspace(tea.MouseWheelMsg{Y: w.bodyStart, Button: tea.MouseWheelDown})
		require.Equal(t, 10, w.offset)
		m.generating = true
		require.NoError(t, w.draw(m, colorprofile.ASCII, true, true))
		busyFooter, _ := pickerNavigationLine(t, strings.Join(w.painted, "\n"), "Ctrl+C")
		require.Equal(t, footerStart, busyFooter)
		require.Less(t, footerStart, w.bodyStart)
	}
}

func TestImagePickerWorkspaceFailurePreservesImageScroll(t *testing.T) {
	m, w := browserForTest(t, 96, 28)
	w.appendImage(imagePickerWorkspaceImage{rows: strings.Split(strings.Repeat("pixels\n", 50), "\n")})
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	w.scroll(7)
	p := &imagePickerInline{model: m, workspace: w, requestCancel: func() {}}
	handled, _ := p.updateWorkspace(imagePickerWorkspaceDone{err: &url.Error{Op: "Post", URL: "http://synthetic.invalid", Err: errors.New("synthetic connection failure")}})
	require.True(t, handled)
	require.NoError(t, p.err)
	w.scroll(1)
	before := w.offset
	w.selectImage(-1)
	require.Equal(t, before, w.offset, "clamped image navigation must not move diagnostics")
	require.Equal(t, 7, w.images[0].inlineOffset)
	require.Contains(t, strings.Join(w.painted, "\n"), "connection to the image service failed")
	p.updateWorkspace(tea.KeyPressMsg{Code: tea.KeyPgDown})
	require.Contains(t, strings.Join(w.painted, "\n"), "Request failed.", "scrolling past diagnostics must retain the failure cue")
	w.expanded = true
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	p.closeExpanded()
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	require.Equal(t, 7, w.images[0].inlineOffset)
}

func TestImagePickerWorkspaceFailedPaintDoesNotStartGeneration(t *testing.T) {
	m, w := browserForTest(t, 96, 28)
	calls := 0
	p := &imagePickerInline{model: m, workspace: w, requestContext: context.Background(), output: &imagePickerOutput{File: w.out, cancel: func() {}}, generate: func(context.Context, *imagePickerResult) error { calls++; return nil }}
	require.NotNil(t, p.startWorkspaceRequest())
	require.Error(t, p.err, "regular files cannot report a terminal size")
	p.stopWorkspaceRequest()
	require.Zero(t, calls, "a failed initial screen must not submit a request")
}

func TestImagePickerWorkspaceValidationVisibleAfterFailure(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {96, 28}} {
		for _, failure := range []string{"request", "open"} {
			for _, scrolled := range []bool{false, true} {
				t.Run(fmt.Sprintf("%dx%d/%s/scrolled=%t", size[0], size[1], failure, scrolled), func(t *testing.T) {
					m, w := browserForTest(t, size[0], size[1])
					w.appendImage(imagePickerWorkspaceImage{rows: strings.Split(strings.Repeat("pixels\n", 50), "\n")})
					p := &imagePickerInline{model: m, workspace: w, requestCancel: func() {}}
					if failure == "request" {
						p.updateWorkspace(imagePickerWorkspaceDone{err: &url.Error{Op: "Post", URL: "http://synthetic.invalid", Err: errors.New("synthetic connection failure")}})
					} else {
						p.updateWorkspace(imagePickerOpened{err: errors.New("synthetic open failure"), index: 0})
					}
					if scrolled {
						p.updateWorkspace(tea.KeyPressMsg{Code: tea.KeyPgDown})
					}
					diagnostics := append([]string(nil), w.rows...)
					offset := w.offset
					m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
					m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
					m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
					m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
					require.Equal(t, "Add a prompt first.", m.note)
					require.Empty(t, m.result.Args, "invalid input must not submit another request")
					require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
					require.Contains(t, ansi.Strip(strings.Join(w.painted, "\n")), m.note)
					require.Equal(t, diagnostics, w.rows, "validation must retain request diagnostics")
					require.Equal(t, offset, w.offset, "validation must preserve gallery scrolling")
					require.Len(t, w.images, 1)
				})
			}
		}
	}
}

func TestImagePickerWorkspaceOpenFailureDoesNotHideLaterValidation(t *testing.T) {
	for _, laterValidation := range []bool{false, true} {
		t.Run(fmt.Sprint(laterValidation), func(t *testing.T) {
			m, w := browserForTest(t, 96, 28)
			w.appendImage(imagePickerWorkspaceImage{file: imageoutput.SavedImage{Path: t.TempDir() + "/missing.png"}})
			messages := make(chan tea.Msg, 1)
			w.send = func(msg tea.Msg) { messages <- msg }
			p := &imagePickerInline{model: m, workspace: w}
			m.settings.prompt = ""
			m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
			require.Equal(t, "Add a prompt first.", m.note)
			p.updateWorkspace(tea.KeyPressMsg{Code: 'o', Text: "o"})
			require.Empty(t, m.note, "a deliberate open starts a new action")
			if laterValidation {
				m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
			}
			p.updateWorkspace(<-messages)
			w.openWorkers.Wait()
			text := ansi.Strip(strings.Join(w.painted, "\n"))
			if laterValidation {
				require.Contains(t, text, "Add a prompt first.")
				require.NotContains(t, text, "Could not open this image.")
			} else {
				require.Contains(t, text, "Could not open this image.")
			}
		})
	}
}

func TestImagePickerWorkspaceFailureKeepsControlsAttachedAndDetailsScrollable(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {80, 24}, {96, 28}} {
		m, w := browserForTest(t, size[0], size[1])
		m.hasImages, m.focus = false, "prompt"
		require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
		footer, _ := pickerNavigationLine(t, strings.Join(w.painted, "\n"), "Ctrl+C")
		m.generating = true
		require.NoError(t, w.draw(m, colorprofile.ASCII, true, true))
		busyFooter, _ := pickerNavigationLine(t, strings.Join(w.painted, "\n"), "Ctrl+C")
		require.Equal(t, footer, busyFooter)
		p := &imagePickerInline{model: m, workspace: w, requestCancel: func() {}}
		p.updateWorkspace(imagePickerWorkspaceDone{err: &url.Error{Op: "Post", URL: "http://synthetic.invalid", Err: errors.New("synthetic connection failure")}})
		failed := strings.Join(w.painted, "\n")
		failedFooter, _ := pickerNavigationLine(t, failed, "Ctrl+C")
		require.Equal(t, footer, failedFooter)
		require.NotContains(t, failed, "Request details")
		require.NotContains(t, failed, "Request failed. Edit")
		require.NotEmpty(t, w.rows)
		// Every diagnostic row remains reachable in the small viewport.
		seen := append([]string(nil), w.painted[w.bodyStart:w.bodyEnd]...)
		for offset := 0; offset < len(w.displayRows); offset++ {
			handled, _ := p.updateWorkspace(tea.KeyPressMsg{Code: tea.KeyPgDown})
			require.True(t, handled)
			seen = append(seen, w.painted[w.bodyStart:w.bodyEnd]...)
		}
		require.Contains(t, strings.Join(seen, " "), "trying again.")
		w.clearRequestDiagnostics()
		m.note = ""
		require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
		require.Zero(t, w.viewportRows)
		require.Len(t, w.painted, len(strings.Split(m.View().Content, "\n")))
	}
}
