package images

import (
	"context"
	"image"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

func TestImagePickerITermFrameAnchorsAndPreservesWrap(t *testing.T) {
	require.Equal(t, "\r"+ansi.SaveCursor, imagePickerITermFrameAnchor(1))
	require.Equal(t, "\r\n\n"+ansi.CursorUp(2)+ansi.SaveCursor, imagePickerITermFrameAnchor(3))
	const content = "one\ntwo\nthree"
	require.Equal(t, ansi.SetModeAutoWrap+terminal.InlineFrame(content, 10)+ansi.RestoreCursor,
		imagePickerITermFrame(content, 10))

	m, w := browserForTest(t, 116, 35)
	w.iterm = true
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	first := nativeCacheOutput(t, w)
	require.Contains(t, first, imagePickerITermFrameAnchor(len(w.painted))+ansi.SetModeAutoWrap)
	m.width, m.height = 70, 20
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	resized := nativeCacheOutput(t, w)
	require.Contains(t, resized, ansi.RestoreCursor+imagePickerITermFrameAnchor(len(w.painted))+ansi.SetModeAutoWrap)
	require.NoError(t, w.close())
	cleanup := nativeCacheOutput(t, w)
	require.Contains(t, cleanup, ansi.RestoreCursor)

	// iTerm2 has one DECSC slot. A second save after DECAWM would overwrite
	// the caller's mode even if the cursor returned to the right cell.
	for _, entryWrap := range []bool{false, true} {
		check := func(frames ...string) {
			wrap, saved := entryWrap, !entryWrap
			for _, frame := range frames {
				for len(frame) > 0 {
					seq, _, size := terminal.Sequence(frame)
					frame = frame[size:]
					switch seq {
					case ansi.SaveCursor:
						saved = wrap
					case ansi.RestoreCursor:
						wrap = saved
					case ansi.SetModeAutoWrap:
						wrap = true
					case ansi.ResetModeAutoWrap:
						wrap = false
					}
				}
			}
			require.Equal(t, entryWrap, wrap)
		}
		check(first)
		check(first, resized)
		// An interrupted first or replacement frame still owns a valid DECSC slot.
		firstPrefix, _, _ := strings.Cut(first, ansi.SetModeAutoWrap)
		check(firstPrefix+ansi.SetModeAutoWrap, cleanup)
		resizedPrefix, _, _ := strings.Cut(resized, ansi.SetModeAutoWrap)
		check(first, resizedPrefix+ansi.SetModeAutoWrap, cleanup)
	}
}

func TestImagePickerITermFailedFirstAnchorLeavesWrapUntouched(t *testing.T) {
	m, w := browserForTest(t, 116, 35)
	w.iterm = true
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	w.context = ctx
	require.Error(t, w.draw(m, colorprofile.ASCII, false, true))
	require.False(t, w.started)
	require.NoError(t, w.close())
	require.Empty(t, nativeCacheOutput(t, w))
	w.context = t.Context()
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	prefix, _, saved := strings.Cut(nativeCacheOutput(t, w), ansi.SaveCursor)
	require.True(t, saved)
	require.NotContains(t, prefix, ansi.RestoreCursor)
}

func TestImagePickerITermGroupsOnlyAdjacentVisibleRows(t *testing.T) {
	m, w := browserForTest(t, 120, 32)
	w.iterm = true
	entry := recoverySavedImage(t)
	entry.protocol, entry.itermBounds = "iterm", image.Rect(0, 0, 32, 32)
	w.images = []imagePickerWorkspaceImage{entry}
	first := imagePickerNativeRow{image: 0, row: 2, columns: 8, count: 6}
	w.nativeFrame = map[int]imagePickerNativeRow{}
	w.nativeReady = map[int]bool{}
	for i := range 3 {
		row := first
		row.row += i
		w.nativeFrame[5+i] = row
	}
	first.region = imagePickerRegion{first: 2, rows: 3}
	prepared, err := prepareITermPlacement(t.Context(), entry, first)
	require.NoError(t, err)
	require.Len(t, prepared.rows, 1)
	w.nativeTransient = map[imagePickerNativeRow]string{first: prepared.rows[0]}
	w.groupITermRegions(make([]string, m.height), 42, true)
	parts := w.itermPlacementParts("chrome", m.height, 42, true)
	require.Equal(t, []string{"chrome", ansi.RestoreCursor + ansi.CursorDown(5) + ansi.CursorHorizontalAbsolute(42), prepared.rows[0]}, parts)
	_, w.nativePainted = w.nativePaintKeys(make([]string, m.height), true)
	require.Len(t, w.nativePainted, 3)
	require.Empty(t, w.itermPlacementParts("edited prompt", m.height, 42, false))
	w.painted = make([]string, m.height)
	w.retainNativeFrame()
	require.Len(t, w.nativeCache, 1)
	require.Equal(t, len(prepared.rows[0]), w.nativeBytes)
	w.nativeTransient = nil
	w.removeNativeRow(first)
	lines := make([]string, m.height)
	w.groupITermRegions(lines, 42, false)
	require.NotContains(t, strings.Join(lines, "\n"), "Preparing preview")
	w.groupITermRegions(lines, 42, true)
	require.Equal(t, 1, strings.Count(strings.Join(lines, "\n"), "Preparing preview…"))
	require.Contains(t, lines[5], "Preparing preview…")

	require.NoError(t, os.WriteFile(entry.file.Path, []byte("replacement"), 0600))
	changed, err := prepareITermPlacement(t.Context(), entry, first)
	require.NoError(t, err)
	require.Empty(t, changed.rows)
	w.invalidateITermImage(0)
	require.Empty(t, w.nativeCache)
	require.Contains(t, w.images[0].rows[0], "Preview unavailable")
}

func TestImagePickerITermPaintReservesCellsBeforeAttachment(t *testing.T) {
	m, w := browserForTest(t, 120, 32)
	w.iterm = true
	entry := recoverySavedImage(t)
	entry.protocol, entry.itermBounds = "iterm", image.Rect(0, 0, 32, 32)
	size := interactiveImageSize{m.width, m.height, 1, 2}
	entry.columns = imagePickerInlinePreviewColumns(entry.itermBounds, size, "iterm")
	entry.nativeRows = imagePickerITermRows(entry.itermBounds, entry.columns, size)
	key := imagePickerNativeRow{columns: entry.columns, count: entry.nativeRows, region: imagePickerRegion{rows: entry.nativeRows}}
	prepared, err := prepareITermPlacement(t.Context(), entry, key)
	require.NoError(t, err)
	entry.rows = prepared.rows
	w.appendImage(entry)
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	frame := nativeCacheOutput(t, w)
	require.Equal(t, 1, strings.Count(frame, "\x1b]1337;File="))
	beforeAttachment, _, found := strings.Cut(frame, prepared.rows[0])
	require.True(t, found)
	_, whileWrapping, enabled := strings.Cut(beforeAttachment, ansi.SaveCursor+ansi.SetModeAutoWrap)
	require.True(t, enabled)
	require.Contains(t, whileWrapping, ansi.RestoreCursor, "restore wrap before a stalled attachment can block cleanup")
	require.Len(t, w.nativeCache, 1)
	m.settings.prompt = "an updated prompt"
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	require.NotContains(t, nativeCacheOutput(t, w), "\x1b]1337;File=")
}
