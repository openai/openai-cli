package images

import (
	"image"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

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
	require.Less(t, strings.Index(frame, ansi.SaveCursor), strings.Index(frame, prepared.rows[0]))
	require.Contains(t, frame, ansi.SaveCursor)
	require.Len(t, w.nativeCache, 1)
	m.settings.prompt = "an updated prompt"
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	require.NotContains(t, nativeCacheOutput(t, w), "\x1b]1337;File=")
}
