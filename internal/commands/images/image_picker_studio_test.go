package images

import (
	"image"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/imageoutput"
	"github.com/stretchr/testify/require"
)

func TestImageStudioPaneSwitchDuringRequest(t *testing.T) {
	m, w := browserForTest(t, 110, 28)
	m.focus, m.selected, m.generating = "options", 3, true
	p := &imagePickerInline{model: m, workspace: w, busy: true}
	for _, expected := range []string{"images", "options"} {
		handled, cmd := p.updateWorkspace(tea.KeyPressMsg{Code: tea.KeyTab})
		require.True(t, handled)
		require.Nil(t, cmd)
		require.Equal(t, expected, m.focus)
		require.Equal(t, 3, m.selected)
	}
	require.Empty(t, m.result.Args)
}

func TestImageStudioReceiptUsesSelectedSaveLocation(t *testing.T) {
	m, w := browserForTest(t, 110, 28)
	home := t.TempDir()
	t.Setenv("HOME", home)
	m.settings.outputDir = "/unrelated/new-draft-folder"
	for _, folder := range []string{"first folder", "second folder"} {
		w.appendImage(imagePickerWorkspaceImage{file: imageoutput.SavedImage{Path: filepath.Join(home, folder, "lake.png")}})
	}
	for i, folder := range []string{"first folder", "second folder"} {
		w.selected = i
		receipt := ansi.Strip(strings.Join(w.studioReceipt(m, 54), "\n"))
		require.Contains(t, receipt, "Saved to ~/"+folder)
		require.NotContains(t, receipt, "new-draft-folder")
	}
	w.images[1].file.Path = "/long/" + strings.Repeat("雪 folder/", 30) + "final\x1b[31m/image.png"
	rows := w.studioReceipt(m, 54)
	require.Len(t, rows, 1)
	for _, row := range rows {
		require.LessOrEqual(t, ansi.StringWidth(row), 54)
	}
	path := ansi.Strip(rows[0])
	require.Contains(t, path, "/long/")
	require.Contains(t, path, "…")
	require.Contains(t, path, "final")
	require.NotContains(t, path, "\x1b")
}

func TestImageStudioKeepsDraftAndControlsBesideOutput(t *testing.T) {
	for _, size := range [][2]int{{110, 28}, {133, 39}, {240, 60}} {
		m, w := browserForTest(t, size[0], size[1])
		m.focus, m.hasImages = "prompt", false
		m.draft, m.settings.prompt, m.cursor = []rune("mountain lake"), "mountain lake", 8
		render := func(busy bool) []string {
			t.Helper()
			m.generating = busy
			require.NoError(t, w.draw(m, colorprofile.ASCII, busy, true))
			return strings.Split(ansi.Strip(strings.Join(w.painted, "\n")), "\n")
		}
		draft := render(false)
		_, ok := imageStudioLayout(m.width, m.height)
		require.True(t, ok)
		cursor := w.header.Cursor
		require.NotNil(t, cursor)
		require.True(t, strings.HasSuffix(ansi.Truncate(draft[cursor.Y], cursor.X, ""), "mountain"))
		require.Contains(t, draft[imageStudioBodyY], "Prompt")
		require.Contains(t, draft[imageStudioBodyY], "Preview")
		require.LessOrEqual(t, len(draft), 18, "the prompt, grouped settings and visible options determine the frame height")
		require.Contains(t, draft[imageStudioBodyY-1], strings.Repeat("─", 30))
		require.NotContains(t, strings.Join(draft, "\n"), "openai images generate")
		w.since = time.Now()
		running := render(true)
		require.Contains(t, running[1], "Generating image")
		require.Nil(t, w.header.Cursor)
		w.appendImage(imagePickerWorkspaceImage{file: imageoutput.SavedImage{Path: "/synthetic/album/lake.png"}, rows: []string{"synthetic pixels"}})
		m.hasImages = true
		complete := render(false)
		repeat := render(true)
		require.Nil(t, w.header.Cursor)
		_ = render(false)
		require.Equal(t, cursor, w.header.Cursor)
		for _, frame := range [][]string{running, complete, repeat} {
			require.Len(t, frame, len(draft))
			for _, label := range []string{"Create image", "Generate", "Settings", "Save to", "Ctrl+C"} {
				before, _ := pickerNavigationLine(t, strings.Join(draft, "\n"), label)
				after, _ := pickerNavigationLine(t, strings.Join(frame, "\n"), label)
				require.Equal(t, before, after, label)
			}
		}
		for _, frame := range [][]string{complete, repeat} {
			require.Contains(t, frame[w.bodyStart], "lake.png")
			require.Contains(t, frame[w.bodyStart], "1 / 1")
			require.NotContains(t, frame[w.bodyStart], "Image")
			require.Contains(t, frame[w.bodyStart+1], "synthetic pixels")
			require.Contains(t, frame[w.bodyEnd], "Saved to /synthetic/album")
		}
		require.LessOrEqual(t, len(complete), m.height)
	}
}

func TestImageStudioBreakpointKeepsGalleryAnchor(t *testing.T) {
	m, w := browserForTest(t, 110, 28)
	for _, name := range []string{"A", "B", "C"} {
		w.appendImage(imagePickerWorkspaceImage{file: imageoutput.SavedImage{Path: name + ".png"}, rows: strings.Split(strings.Repeat(name+"\n", 40), "\n")})
	}
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	w.selectImage(1)
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	w.scroll(6)
	anchor := w.offset - w.galleryStart(1)
	require.Greater(t, anchor, 0)
	for _, size := range [][2]int{{110, 27}, {109, 28}, {110, 28}} {
		m.width, m.height = size[0], size[1]
		require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
		require.Equal(t, anchor, w.offset-w.galleryStart(1))
		require.Equal(t, "B", w.displayRows[w.offset])
	}
}

func TestImageStudioFitsLandscapeFontPreview(t *testing.T) {
	m, w := browserForTest(t, 133, 39)
	g, _ := imageStudioLayout(m.width, m.height)
	columns := imagePickerInlinePreviewColumns(image.Rect(0, 0, 512, 320), interactiveImageSize{133, 39, 8, 16}, "font")
	require.Equal(t, 41, columns, "use the three reclaimed metadata rows")
	rows := strings.Split(strings.TrimSuffix(strings.Repeat(strings.Repeat("x", columns)+"\n", 13), "\n"), "\n")
	w.appendImage(imagePickerWorkspaceImage{file: imageoutput.SavedImage{Path: "/sample/album/lake.png"},
		protocol: "font", rows: rows, columns: columns})
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	require.Equal(t, 20, len(w.painted), "the previous 32-column sample also occupied 20 rows")
	require.Equal(t, 120, g.frameWidth)
	require.Contains(t, w.painted[w.bodyEnd-1], rows[12])
	require.Contains(t, w.painted[w.bodyEnd], "Saved to /sample/album")
	require.Contains(t, w.painted[w.bodyEnd+1], "╰")
	require.Contains(t, w.painted[w.bodyEnd+2], "Ctrl+C")
}

func TestImageStudioShortcutsBelowFrame(t *testing.T) {
	for _, size := range [][2]int{{133, 39}, {80, 24}} {
		m, w := browserForTest(t, size[0], size[1])
		m.focus = "prompt"
		require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
		rows := strings.Split(ansi.Strip(strings.Join(w.painted, "\n")), "\n")
		border, shortcuts := -1, -1
		for i, row := range rows {
			if strings.Contains(row, "╰") {
				border = i
			}
			if strings.Contains(row, "Ctrl+C") {
				shortcuts = i
				require.NotContains(t, row, "│")
			}
		}
		require.GreaterOrEqual(t, border, 0)
		require.Greater(t, shortcuts, border)
	}
}

func TestImageStudioCarouselKeepsControlsBesideLongFilenames(t *testing.T) {
	m, w := browserForTest(t, 120, 30)
	m.color = false
	for _, suffix := range []string{"-one.png", "-two.png", "-three.png"} {
		w.appendImage(imagePickerWorkspaceImage{file: imageoutput.SavedImage{Path: "/album/" + strings.Repeat("山", 30) + suffix}, rows: []string{"pixels"}})
	}
	for i, tail := range []string{"-one.png", "-two.png", "-three.png"} {
		require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
		caption := ansi.Strip(w.painted[w.bodyStart])
		require.Contains(t, caption, tail)
		pager := ansi.Strip(w.studioPager(m))
		require.Equal(t, i > 0, strings.Contains(pager, "‹"))
		require.Equal(t, i < 2, strings.Contains(pager, "›"))
		require.NotContains(t, strings.Join(w.painted, "\n"), "Expand  Open file")
		w.selectImage(1)
	}
}

func TestImageStudioShowsRecoveryInstructionsWithoutClipping(t *testing.T) {
	m, w := browserForTest(t, 110, 28)
	w.appendImage(imagePickerWorkspaceImage{rows: []string{"original pixels"}})
	m.setNote("Remove the NUL character; shell arguments cannot contain it.", imageWarning)
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	require.Contains(t, ansi.Strip(strings.Join(w.painted[:imageStudioBodyY], "\n")), m.note)
	require.Contains(t, strings.Join(w.painted, "\n"), "original pixels")

	m.setNote(strings.Repeat("abcdefghijklmnop", 90)+" final recovery step", imageWarning)
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	// Wrapping may put the last three words on different terminal rows.
	words := strings.Fields(strings.Join(w.displayRows[:w.messageRows], " "))
	require.GreaterOrEqual(t, len(words), 3)
	require.Equal(t, []string{"final", "recovery", "step"}, words[len(words)-3:])
	start := w.offset
	w.scroll(w.viewportRows)
	require.Greater(t, w.offset, start)
	w.scroll(len(w.displayRows))
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	require.LessOrEqual(t, w.messageRows, w.offset+w.viewportRows)
	for at := w.offset; at < w.messageRows; at++ {
		require.Contains(t, w.painted[w.bodyStart+at-w.offset], w.displayRows[at])
	}
}

func TestImageStudioWrappedGuidanceKeepsItsTone(t *testing.T) {
	m, w := browserForTest(t, 110, 28)
	m.color, m.dark = true, true
	w.addDetail(strings.Repeat("Try again. ", 14)+"Reopen Images.", imageSecondary)
	require.NoError(t, w.draw(m, colorprofile.TrueColor, false, true))
	require.Greater(t, w.messageRows, 1)
	for _, row := range w.displayRows[:w.messageRows] {
		require.Equal(t, m.workspaceText(ansi.Strip(row), imageSecondary), row)
	}
}

func TestImageStudioCompactGalleryShowsHowToExpand(t *testing.T) {
	m, w := browserForTest(t, 80, 24)
	m.focus = "prompt"
	w.appendImage(imagePickerWorkspaceImage{rows: []string{"sample"}})
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	require.Contains(t, strings.Join(w.painted, "\n"), "Shift+Tab images")
	m.focus = "images"
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	require.Contains(t, strings.Join(w.painted, "\n"), "Enter expand")
}

func TestImageStudioCompactHintsKeepFormHeightAtWrapBoundary(t *testing.T) {
	for _, width := range []int{68, 69} {
		m, w := browserForTest(t, width, 26)
		m.hasImages, m.focus = false, "prompt"
		require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
		formRows := w.top
		m.generating = true
		require.NoError(t, w.draw(m, colorprofile.ASCII, true, true))
		require.Equal(t, formRows, w.top)
		w.appendImage(imagePickerWorkspaceImage{rows: []string{"sample"}})
		m.hasImages = true
		require.NoError(t, w.draw(m, colorprofile.ASCII, true, true))
		require.Equal(t, formRows, w.top)
		m.generating = false
		require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
		require.Equal(t, formRows, w.top)
		require.Contains(t, strings.Join(w.painted, "\n"), "Shift+Tab images")
	}
}
