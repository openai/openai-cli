package images

import (
	"context"
	"errors"
	"image"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/openai/openai-cli/internal/terminalimage"
)

// Reserve the panel before saving its origin. At the bottom of the terminal,
// these line feeds scroll first, while the saved cursor still belongs to the caller.
func imagePickerITermFrameAnchor(rows int) string {
	if rows <= 1 {
		return "\r" + ansi.SaveCursor
	}
	return "\r" + strings.Repeat("\n", rows-1) + ansi.CursorUp(rows-1) + ansi.SaveCursor
}

// iTerm2's DECSC saved both the panel origin and the incoming wrap mode.
// Restore both before positioning an image or handing input back to the shell.
func imagePickerITermFrame(content string, width int) string {
	return ansi.SetModeAutoWrap + terminal.InlineFrame(content, width) + ansi.RestoreCursor
}

// A region identifies the source rows visible together in one iTerm2 placement.
type imagePickerRegion struct{ first, rows int }

func (row imagePickerNativeRow) payloadKey() imagePickerNativeRow {
	if row.region.rows > 0 {
		row.row = row.region.first
	}
	return row
}

func imagePickerITermRows(bounds image.Rectangle, columns int, size interactiveImageSize) int {
	if columns < 1 {
		return 0
	}
	height := int64(bounds.Dy()) * int64(columns) * int64(size.cellWidth)
	width := int64(bounds.Dx()) * int64(size.cellHeight)
	return max(1, int((height+width-1)/width))
}

// Retain logical rows for scrolling. A resize refits from validated source
// dimensions; pixel preparation stays outside the event loop.
func (w *imagePickerWorkspace) fitITermImages(m *imagePicker) {
	cw, ch := terminalimage.CellSize(w.out.Fd())
	size := interactiveImageSize{m.width, m.height, cw, ch}
	if size != w.itermViewport {
		for index := range w.images {
			w.fitITermImage(index, size)
		}
		w.itermViewport = size
	}
	if w.expanded && len(w.images) > 0 && (w.expandedIndex != w.selected || w.expandedSize != size) {
		bounds := w.images[w.selected].itermBounds
		if !bounds.Empty() {
			w.expandedIndex, w.expandedSize = w.selected, size
			w.expandedColumns = savedImagePreviewColumns(bounds, size.width-5, size.height-2, cw, ch)
			w.expandedNativeRows = imagePickerITermRows(bounds, w.expandedColumns, size)
			w.expandedRows = nil
			w.discardNativeGeometry(w.selected, true, w.expandedColumns, w.expandedNativeRows)
		}
	}
}

func (w *imagePickerWorkspace) fitITermImage(index int, size interactiveImageSize) {
	entry := &w.images[index]
	if entry.itermBounds.Empty() {
		return
	}
	columns := imagePickerInlinePreviewColumns(entry.itermBounds, size, entry.protocol)
	if _, wide := imageStudioLayout(size.width, size.height); !wide && size.width >= 40 && size.height >= 12 {
		// The stacked gallery scrolls. Preserve its prepared width when changing
		// layouts, even if only a few source rows fit below the editor.
		if entry.columns > 0 {
			columns = min(entry.columns, imagePickerInlinePreviewWidth(size))
		} else {
			columns = max(1, columns)
		}
	}
	count := imagePickerITermRows(entry.itermBounds, columns, size)
	if entry.columns != columns || entry.nativeRows != count {
		entry.columns, entry.nativeRows = columns, count
		w.discardNativeGeometry(index, false, columns, count)
		w.galleryRevision++
	}
	entry.unavailableSize = nil
	if columns == 0 {
		entry.unavailableSize = &size
	}
}

func prepareITermPlacement(ctx context.Context, entry imagePickerWorkspaceImage, key imagePickerNativeRow) (imagePickerExpandedRows, error) {
	result := imagePickerExpandedRows{placement: key}
	img, err := terminalimage.ReadSavedMatching(ctx, entry.file.Path, entry.file.SHA256)
	if err != nil {
		return result, nil // An unavailable or replaced saved file cannot supply a new crop.
	}
	var payload strings.Builder
	err = terminalimage.WriteITermRegion(ctx, &payload, img, key.columns, key.count, key.region.first, key.region.rows)
	if err == nil {
		result.rows = []string{payload.String()}
	}
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return result, nil
	}
	return result, err
}

// Layout may clip the beginning or end of an image. Bind each contiguous
// run after clipping so one PNG covers precisely the rows still on screen.
func (w *imagePickerWorkspace) groupITermRegions(lines []string, column int, repaint bool) {
	height := len(lines)
	for top := 0; top < height; top++ {
		first, ok := w.nativeFrame[top]
		if !ok || w.images[first.image].itermBounds.Empty() {
			continue
		}
		end := top + 1
		for end < height {
			next, found := w.nativeFrame[end]
			if !found || next.image != first.image || next.expanded != first.expanded || next.row != first.row+end-top {
				break
			}
			end++
		}
		first.region = imagePickerRegion{first.row, end - top}
		_, ready := w.nativePayload(first)
		previous, painted := w.nativePainted[top]
		if !ready && (repaint || !painted || previous != first) {
			const notice = "Preparing preview…"
			if chrome, ok := w.nativeChrome[top]; ok {
				lines[top] = chrome.prefix + notice + chrome.suffix
			} else {
				// Stacked and expanded views use the same four-column margin.
				lines[top] += ansi.CursorHorizontalAbsolute(column) + notice
			}
		}
		for y := top; y < end; y++ {
			row := first
			row.row += y - top
			w.nativeFrame[y], w.nativeReady[y] = row, ready
		}
		top = end - 1
	}
}

// The text frame reserves all cells and saves its origin before a multirow
// attachment moves the terminal cursor. Later editor paints keep it intact.
func (w *imagePickerWorkspace) itermPlacementParts(text string, height, column int, repaint bool) []string {
	var parts []string
	for y := 0; y < height; y++ {
		row, ok := w.nativeFrame[y]
		if !ok || row.region.rows == 0 || row.row != row.region.first || !w.nativeReady[y] {
			continue
		}
		if old, painted := w.nativePainted[y]; !repaint && painted && old == row {
			continue
		}
		if len(parts) == 0 {
			parts = append(parts, text)
		}
		position := ansi.RestoreCursor
		if y > 0 {
			position += ansi.CursorDown(y)
		}
		payload, _ := w.nativePayload(row)
		parts = append(parts, position+ansi.CursorHorizontalAbsolute(column), payload)
	}
	return parts
}

func (w *imagePickerWorkspace) invalidateITermImage(index int) {
	entry := &w.images[index]
	entry.itermBounds, entry.columns, entry.nativeRows = image.Rectangle{}, 0, 0
	entry.rows = []string{"Preview unavailable. Open the saved file; no need to generate again."}
	w.discardNativeGeometry(index, false, 0, 0)
	w.discardNativeGeometry(index, true, 0, 0)
	if w.expandedIndex == index {
		w.expandedIndex = -1
		w.expandedRows, w.expandedNativeRows, w.expandedColumns = nil, 0, 0
	}
	w.galleryRevision++
}

func (w *imagePickerWorkspace) itermScrollOffset() int {
	if w.expanded && w.selected >= 0 && w.selected < len(w.images) {
		return w.images[w.selected].offset
	}
	return w.offset
}
