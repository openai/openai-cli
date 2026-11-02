package images

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/imageoutput"
	"github.com/stretchr/testify/require"
)

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
		require.Contains(t, strings.Join(draft, "\n"), "--prompt 'mountain lake'")
		w.since = time.Now()
		running := render(true)
		require.Contains(t, running[imageStudioBodyY], "Generating image")
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
			for _, label := range []string{"Create image", "Generate image", "Settings", "Save to", "Ctrl+C"} {
				before, _ := pickerNavigationLine(t, strings.Join(draft, "\n"), label)
				after, _ := pickerNavigationLine(t, strings.Join(frame, "\n"), label)
				require.Equal(t, before, after, label)
			}
		}
		for _, frame := range [][]string{complete, repeat} {
			require.Contains(t, frame[w.bodyStart], "lake.png")
			require.Contains(t, frame[w.bodyStart+1], "synthetic pixels")
			require.Contains(t, strings.Join(frame, "\n"), "Saved in album")
			require.NotContains(t, strings.Join(frame, "\n"), "/synthetic/")
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
	w.scroll(48)
	anchor := w.offset - w.galleryStarts[1]
	require.Greater(t, anchor, 0)
	for _, size := range [][2]int{{110, 27}, {109, 28}, {110, 28}} {
		m.width, m.height = size[0], size[1]
		require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
		require.Equal(t, anchor, w.offset-w.galleryStarts[1])
		require.Equal(t, "B", w.displayRows[w.offset])
	}
}

func TestImageStudioShowsRecoveryInstructionsWithoutClipping(t *testing.T) {
	m, w := browserForTest(t, 110, 28)
	w.appendImage(imagePickerWorkspaceImage{rows: []string{"original pixels"}})
	m.setNote("Remove the NUL character; shell arguments cannot contain it.", imageWarning)
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	require.Contains(t, ansi.Strip(w.painted[imageStudioBodyY-1]), m.note)
	require.Contains(t, strings.Join(w.painted, "\n"), "original pixels")

	m.setNote(strings.Repeat("abcdefghijklmnop", 90)+" final recovery step", imageWarning)
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	text := strings.Join(w.displayRows[:w.messageRows], "")
	require.Contains(t, text, "final recovery step")
	start := w.offset
	w.scroll(w.viewportRows)
	require.Greater(t, w.offset, start)
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	require.Contains(t, strings.Join(w.painted, "\n"), "final recovery step")
}

func TestImageStudioWrappedGuidanceKeepsItsTone(t *testing.T) {
	m, w := browserForTest(t, 110, 28)
	m.color, m.dark = true, true
	w.rows = []string{strings.Repeat("Try again. ", 14) + "Reopen Images."}
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
