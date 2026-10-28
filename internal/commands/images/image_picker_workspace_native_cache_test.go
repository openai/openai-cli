package images

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/require"
)

func nativeCacheTestRow(protocol string, columns, payload int) string {
	if protocol == "kitty" {
		return fmt.Sprintf("\x1b_Ga=T,c=%d,r=1;%s\x1b\\", columns, strings.Repeat("A", payload))
	}
	return fmt.Sprintf("\x1b]1337;File=inline=1;width=%d;height=1:%s\a", columns, strings.Repeat("A", payload))
}

// All native payloads belong to one map. Counting its entries conservatively
// includes aliases; other retained roots must contain neither rows nor wrappers.
func requireNativeCacheBound(t *testing.T, w *imagePickerWorkspace) {
	t.Helper()
	cacheBytes := 0
	for _, payload := range w.nativeCache {
		cacheBytes += len(payload.text)
	}
	for _, rows := range [][]string{w.rows, w.displayRows, w.expandedRows, w.painted} {
		for _, text := range rows {
			require.NotContains(t, text, "\x1b_G", "layout and paint state must contain no payload copies")
			require.NotContains(t, text, "\x1b]1337;", "layout and paint state must contain no payload copies")
		}
	}
	for _, entry := range w.images {
		for _, text := range entry.rows {
			require.NotContains(t, text, "\x1b_G")
			require.NotContains(t, text, "\x1b]1337;")
		}
		if entry.nativeRows > 0 {
			require.Empty(t, entry.rows)
		}
	}
	require.Empty(t, w.nativeTransient, "draw must release the completion handoff")
	require.Equal(t, cacheBytes, w.nativeBytes)
	require.LessOrEqual(t, cacheBytes, imagePickerNativeCacheBytes)
}

func nativeCacheOutput(t *testing.T, w *imagePickerWorkspace) string {
	t.Helper()
	data, err := os.ReadFile(w.out.Name())
	require.NoError(t, err)
	require.NoError(t, w.out.Truncate(0))
	_, err = w.out.Seek(0, 0)
	require.NoError(t, err)
	require.NotContains(t, string(data), "\x00native:", "identities must never reach the terminal")
	return string(data)
}

func TestImagePickerNativeCacheBoundsRepeatedGenerations(t *testing.T) {
	for _, protocol := range []string{"kitty", "iterm"} {
		t.Run(protocol, func(t *testing.T) {
			m, w := browserForTest(t, 96, 28)
			for i := range 12 {
				row := nativeCacheTestRow(protocol, 32, 1<<20)
				w.appendImage(imagePickerWorkspaceImage{protocol: protocol, columns: 32, rows: []string{row}})
				w.selected, w.galleryJump = i, true
				w.stageNativeRows(i, false, 32, []string{row})
				require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
				require.Contains(t, nativeCacheOutput(t, w), row)
				requireNativeCacheBound(t, w)
			}
			require.Len(t, w.images, 12)
			require.Less(t, len(w.nativeCache), len(w.images))
			require.Equal(t, 1, w.images[0].rowCount(), "eviction must preserve geometry")
		})
	}
}

func TestImagePickerNativeCacheEvictsOptionalRowsBeforeVisibleAdmission(t *testing.T) {
	m, w := browserForTest(t, 96, 28)
	old := nativeCacheTestRow("kitty", 32, 5<<20)
	oldRows := make([]string, 40)
	for i := range oldRows {
		oldRows[i] = nativeCacheTestRow("kitty", 32, 20)
	}
	oldRows[0] = old
	w.appendImage(imagePickerWorkspaceImage{columns: 32, rows: oldRows})
	row := nativeCacheTestRow("kitty", 32, 4<<20)
	w.appendImage(imagePickerWorkspaceImage{columns: 32, rows: []string{row}})
	w.selected, w.galleryJump = 1, true
	w.stageNativeRows(1, false, 32, []string{row})
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	require.Contains(t, nativeCacheOutput(t, w), row)
	_, present := w.nativeCache[imagePickerNativeRow{1, 0, 32, 1, false}]
	require.True(t, present, "a row that fits alone must evict older optional payload")
	_, present = w.nativeCache[imagePickerNativeRow{0, 0, 32, 40, false}]
	require.False(t, present, "the offscreen payload must yield to the visible row")
	requireNativeCacheBound(t, w)
}

func TestImagePickerNativeCacheExactWireByteBoundary(t *testing.T) {
	for _, protocol := range []string{"kitty", "iterm"} {
		for _, delta := range []int{-1, 0, 1} {
			t.Run(fmt.Sprintf("%s/%+d", protocol, delta), func(t *testing.T) {
				framing := len(nativeCacheTestRow(protocol, 32, 0))
				row := nativeCacheTestRow(protocol, 32, imagePickerNativeCacheBytes-framing+delta)
				require.Len(t, row, imagePickerNativeCacheBytes+delta)
				w := &imagePickerWorkspace{}
				w.appendImage(imagePickerWorkspaceImage{columns: 32, rows: []string{row}})
				_, admitted := w.nativeCache[imagePickerNativeRow{0, 0, 32, 1, false}]
				require.Equal(t, delta <= 0, admitted)
				require.Equal(t, 1, w.images[0].rowCount())
				requireNativeCacheBound(t, w)
			})
		}
	}
}

func TestImagePickerNativeCacheFindsVisibleMissWithOffscreenSelection(t *testing.T) {
	m, w := browserForTest(t, 96, 28)
	entry := recoverySavedImage(t)
	entry.protocol, entry.columns = "kitty", 32
	row := nativeCacheTestRow("kitty", 32, 20)
	entry.rows = make([]string, 40)
	for i := range entry.rows {
		entry.rows[i] = row
	}
	w.appendImage(entry)
	w.appendImage(imagePickerWorkspaceImage{columns: 90, nativeRows: 40, protocol: "kitty"})
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	for key := range w.nativeCache {
		w.removeNativeRow(key)
	}
	w.selected = 1 // Preserve the viewport while selection moves outside it.
	w.nativePainted = nil
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	missing, ok := w.missingNativeRow()
	require.True(t, ok)
	require.Equal(t, 0, missing.image)
	require.Equal(t, 1, w.selected)
}

func TestImagePickerNativeCacheOversizedRowsRetainOnlySuccessfulPlacements(t *testing.T) {
	for _, protocol := range []string{"kitty", "iterm"} {
		t.Run(protocol, func(t *testing.T) {
			m, w := browserForTest(t, 96, 28)
			row := nativeCacheTestRow(protocol, 32, imagePickerNativeCacheBytes)
			w.appendImage(imagePickerWorkspaceImage{protocol: protocol, columns: 32, rows: []string{row, row}})
			w.stageNativeRows(0, false, 32, []string{row, row})
			require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
			require.Equal(t, 2, strings.Count(nativeCacheOutput(t, w), row))
			requireNativeCacheBound(t, w)
			require.Empty(t, w.nativeCache)
			_, missing := w.missingNativeRow()
			require.False(t, missing)
			require.Equal(t, protocol == "kitty", w.kitty)
			require.NoError(t, w.draw(m, colorprofile.ASCII, true, false))
			require.NotContains(t, nativeCacheOutput(t, w), "Preparing preview")
			_, missing = w.missingNativeRow()
			require.False(t, missing, "unchanged ticks must not reload a successfully painted oversized row")
			m.width--
			require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
			require.Contains(t, nativeCacheOutput(t, w), "Preparing preview")
			_, missing = w.missingNativeRow()
			require.True(t, missing, "resizing invalidates uncached placements")
			w.stageNativeRows(0, false, 32, []string{row, row})
			require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
			require.Equal(t, 2, strings.Count(nativeCacheOutput(t, w), row), "completion must replace painted placeholders")
			requireNativeCacheBound(t, w)
		})
	}
}

func TestImagePickerNativeCacheReloadsAfterScrollAndViewChanges(t *testing.T) {
	m, w := browserForTest(t, 96, 28)
	row := nativeCacheTestRow("kitty", 32, 20)
	rows := make([]string, 30)
	for i := range rows {
		rows[i] = row
	}
	w.appendImage(imagePickerWorkspaceImage{columns: 32, rows: rows})
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	nativeCacheOutput(t, w)
	for key := range w.nativeCache {
		w.removeNativeRow(key)
	}
	w.scroll(3)
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	first, missing := w.missingNativeRow()
	require.True(t, missing)
	require.Greater(t, first.row, 0)
	w.stageNativeRows(0, false, 32, rows)
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	w.expanded = true
	w.expandedIndex, w.expandedSize = 0, interactiveImageSize{width: 96, height: 28}
	w.expandedNativeRows, w.expandedColumns = len(rows), 32
	w.stageNativeRows(0, true, 32, rows)
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	requireNativeCacheBound(t, w)
	p := &imagePickerInline{workspace: w}
	p.closeExpanded()
	for key := range w.nativeCache {
		w.removeNativeRow(key)
	}
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	first, missing = w.missingNativeRow()
	require.True(t, missing, "Back must not reuse expanded placements as inline placements")
	require.False(t, first.expanded)
	// A changed header moves the same logical rows to new screen positions.
	w.stageNativeRows(0, false, 32, rows)
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	for key := range w.nativeCache {
		w.removeNativeRow(key)
	}
	w.header.Content = "short header"
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	_, missing = w.missingNativeRow()
	require.True(t, missing)
	requireNativeCacheBound(t, w)
}

func TestImagePickerNativeCacheWriteFailureDoesNotRecordPlacement(t *testing.T) {
	m, w := browserForTest(t, 96, 28)
	row := nativeCacheTestRow("kitty", 32, 20)
	w.appendImage(imagePickerWorkspaceImage{columns: 32, rows: []string{row}})
	w.stageNativeRows(0, false, 32, []string{row})
	require.NoError(t, w.out.Close())
	require.Error(t, w.draw(m, colorprofile.ASCII, false, true))
	require.Empty(t, w.nativePainted)
	require.Empty(t, w.painted)
	requireNativeCacheBound(t, w)
}

func TestImagePickerNativeReloadPreservesGeometryAndFailureStates(t *testing.T) {
	entry := recoverySavedImage(t)
	entry.protocol, entry.nativeRows, entry.columns, entry.inlineOffset = "kitty", 20, 32, 8
	w := &imagePickerWorkspace{images: []imagePickerWorkspaceImage{entry}}
	prepare := func(_ context.Context, _ *os.File, _ image.Image, protocol string, columns int) ([]string, string, error) {
		require.Equal(t, 32, columns)
		return []string{nativeCacheTestRow(protocol, columns, 20)}, protocol, nil
	}
	result, err := imagePickerPrepareReload(t.Context(), nil, entry, entry.columns, prepare)
	require.NoError(t, err)
	w.replaceImageRows(result)
	require.Equal(t, 1, w.images[0].nativeRows, "changed cell geometry makes the returned count authoritative")
	require.Equal(t, 8, w.images[0].inlineOffset)
	small := interactiveImageSize{width: 8, height: 4}
	w.replaceImageRows(imagePickerExpandedRows{index: 0, size: small, viewportUnavailable: true})
	require.Equal(t, 1, w.images[0].rowCount())
	require.Equal(t, &small, w.images[0].unavailableSize)
	require.Equal(t, -1, w.inlineResizeIndex(small))
	require.Equal(t, 0, w.inlineResizeIndex(interactiveImageSize{width: 96, height: 28}))
	for _, disabled := range []bool{false, true} {
		if disabled {
			entry.protocol = ""
		} else {
			require.NoError(t, os.WriteFile(entry.file.Path, []byte("replacement"), 0600))
		}
		result, err = imagePickerPrepareReload(t.Context(), nil, entry, 32, func(context.Context, *os.File, image.Image, string, int) ([]string, string, error) {
			t.Fatal("replaced files and disabled previews must not reach the encoder")
			return nil, "", nil
		})
		require.NoError(t, err)
		w.replaceImageRows(result)
		require.Zero(t, w.images[0].nativeRows)
		require.NotEmpty(t, w.images[0].rows)
		require.Equal(t, -1, w.inlineResizeIndex(interactiveImageSize{width: 96, height: 28}))
	}
}

func TestImagePickerNativeReloadCancellationPreservesOtherFailures(t *testing.T) {
	for _, unsafeFailure := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		entry := recoverySavedImage(t)
		entry.protocol = "kitty"
		started, done := make(chan struct{}), make(chan error, 1)
		failure := errors.New("synthetic unsafe failure")
		go func() {
			_, err := imagePickerPrepareReload(ctx, nil, entry, 32, func(ctx context.Context, _ *os.File, _ image.Image, _ string, _ int) ([]string, string, error) {
				close(started)
				<-ctx.Done()
				if unsafeFailure {
					return nil, "kitty", errors.Join(ctx.Err(), failure)
				}
				return nil, "kitty", ctx.Err()
			})
			done <- err
		}()
		<-started
		p := &imagePickerInline{workspace: &imagePickerWorkspace{expanded: true, previewCancel: cancel}}
		p.closeExpanded()
		err := <-done
		if unsafeFailure {
			require.ErrorIs(t, err, failure)
			require.ErrorIs(t, err, context.Canceled)
		} else {
			require.NoError(t, err, "navigation cancellation must not become a fatal preview failure")
		}
	}
}

func TestImagePickerNativeCacheRejectsStaleAndOffscreenCompletions(t *testing.T) {
	m, w := browserForTest(t, 96, 28)
	row := nativeCacheTestRow("kitty", 32, 20)
	rows := make([]string, 40)
	for i := range rows {
		rows[i] = row
	}
	w.appendImage(imagePickerWorkspaceImage{columns: 32, rows: rows})
	w.appendImage(imagePickerWorkspaceImage{columns: 32, nativeRows: 1})
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	w.previewRevision = 2
	p := &imagePickerInline{workspace: w, model: m}
	for _, result := range []imagePickerExpandedRows{
		{revision: 1, index: 1, inline: true, columns: 32, rows: []string{row}},
		{revision: 2, index: 1, inline: true, columns: 32, rows: []string{row}, size: interactiveImageSize{width: 90, height: 28}},
		{revision: 2, index: 1, inline: true, columns: 32, rows: []string{row}},
	} {
		p.updateWorkspace(result)
		_, present := w.nativeCache[imagePickerNativeRow{1, 0, 32, 1, false}]
		require.False(t, present)
		requireNativeCacheBound(t, w)
	}
}
