package images

import (
	"fmt"
	"strings"

	"github.com/openai/openai-cli/internal/terminalimage"
)

// Native rows contain complete PNG transfers. Keep their retained payload in one
// bounded map; layout and painted frames keep identities, never payload copies.
const imagePickerNativeCacheBytes = 8 << 20

type imagePickerNativeRow struct {
	image, row, columns, count int
	expanded                   bool
}

type imagePickerNativePayload struct {
	text string
	used uint64
}

func imagePickerNativeRows(rows []string) bool {
	if len(rows) == 0 {
		return false
	}
	for _, row := range rows {
		if !strings.HasPrefix(row, "\x1b_G") && !strings.HasPrefix(row, "\x1b]1337;") {
			return false
		}
	}
	return true
}

func (entry imagePickerWorkspaceImage) rowCount() int {
	if entry.nativeRows > 0 {
		return entry.nativeRows
	}
	return len(entry.rows)
}

func (w *imagePickerWorkspace) expandedRowCount() int {
	if w.expandedNativeRows > 0 {
		return w.expandedNativeRows
	}
	return len(w.expandedRows)
}

func (row imagePickerNativeRow) identity() string {
	return fmt.Sprintf("\x00native:%d:%d:%d:%d:%t", row.image, row.row, row.columns, row.count, row.expanded)
}

func (w *imagePickerWorkspace) nativePayload(row imagePickerNativeRow) (string, bool) {
	if text, ok := w.nativeTransient[row]; ok {
		return text, true
	}
	value, ok := w.nativeCache[row]
	return value.text, ok
}

// A completion may temporarily supply a full prepared image. draw releases this
// handoff before returning, including rows too large for the retained cache.
func (w *imagePickerWorkspace) stageNativeRows(index int, expanded bool, columns int, rows []string) {
	w.nativeTransient = nil
	if !imagePickerNativeRows(rows) {
		return
	}
	w.nativeTransient = make(map[imagePickerNativeRow]string, len(rows))
	for i, text := range rows {
		w.nativeTransient[imagePickerNativeRow{index, i, columns, len(rows), expanded}] = text
	}
}

func (w *imagePickerWorkspace) removeNativeRow(row imagePickerNativeRow) {
	w.nativeBytes -= len(w.nativeCache[row].text)
	delete(w.nativeCache, row)
}

func (w *imagePickerWorkspace) discardNativeGeometry(index int, expanded bool, columns, count int) {
	for row := range w.nativeCache {
		if row.image == index && row.expanded == expanded && (row.columns != columns || row.count != count) {
			w.removeNativeRow(row)
		}
	}
}

// New, potentially offscreen arrivals may use spare space. They never displace
// the visible working set before the UI decides whether to follow the arrival.
func (w *imagePickerWorkspace) cacheNativeArrival(index int, columns int, rows []string) {
	for i, text := range rows {
		if len(text) <= imagePickerNativeCacheBytes-w.nativeBytes {
			w.storeNativeRow(imagePickerNativeRow{index, i, columns, len(rows), false}, text)
		}
	}
}

func (w *imagePickerWorkspace) storeNativeRow(row imagePickerNativeRow, text string) {
	if w.nativeCache == nil {
		w.nativeCache = make(map[imagePickerNativeRow]imagePickerNativePayload)
	}
	w.nativeClock++
	w.nativeBytes += len(text) - len(w.nativeCache[row].text)
	w.nativeCache[row] = imagePickerNativePayload{text, w.nativeClock}
}

func (w *imagePickerWorkspace) nativeViewRow(row imagePickerNativeRow, screenRow int) string {
	if text, ok := w.nativePayload(row); ok {
		if cells := terminalimage.RowColumns(text); cells < 0 || cells > row.columns {
			return ""
		}
		w.nativeFrame[screenRow] = row
		w.nativeReady[screenRow] = true
		return text
	}
	w.nativeFrame[screenRow] = row
	return "Preparing preview…"
}

// Only a successfully emitted row can stand in for an uncached payload. A full
// repaint clears old placements, so every absent payload becomes a fresh miss.
func (w *imagePickerWorkspace) nativePaintKeys(lines []string, repaint bool) ([]string, map[int]imagePickerNativeRow) {
	keys := append([]string(nil), lines...)
	placements := make(map[int]imagePickerNativeRow)
	for y, row := range w.nativeFrame {
		if y >= len(keys) {
			continue
		}
		previous, present := w.nativePainted[y]
		if w.nativeReady[y] || !repaint && present && previous == row {
			keys[y] = row.identity()
			placements[y] = row
		}
	}
	return keys, placements
}

// Admit only rows in the current frame, after writing succeeds. Offscreen worker
// completions cannot refill the cache. Oversized rows remain transient and their
// successful placement identity prevents repeated decoding on unchanged ticks.
func (w *imagePickerWorkspace) retainNativeFrame() {
	visible := make(map[imagePickerNativeRow]bool, len(w.nativeFrame))
	for _, row := range w.nativeFrame {
		visible[row] = true
	}
	for y := 0; y < len(w.painted); y++ {
		row, ok := w.nativeFrame[y]
		if !ok || !w.nativeReady[y] {
			continue
		}
		text, ok := w.nativePayload(row)
		if !ok || len(text) > imagePickerNativeCacheBytes {
			continue
		}
		needed := len(text) - len(w.nativeCache[row].text)
		for needed > imagePickerNativeCacheBytes-w.nativeBytes {
			var oldest imagePickerNativeRow
			found := false
			for key, payload := range w.nativeCache {
				if key == row {
					continue
				}
				if !found || visible[oldest] && !visible[key] || visible[oldest] == visible[key] && payload.used < w.nativeCache[oldest].used {
					oldest, found = key, true
				}
			}
			if !found {
				break
			}
			w.removeNativeRow(oldest)
		}
		if needed <= imagePickerNativeCacheBytes-w.nativeBytes {
			w.storeNativeRow(row, text)
		}
	}
}

func (w *imagePickerWorkspace) missingNativeRow() (imagePickerNativeRow, bool) {
	for y := 0; y < len(w.painted); y++ {
		row, ok := w.nativeFrame[y]
		painted, present := w.nativePainted[y]
		if ok && (!present || painted != row) {
			return row, true
		}
	}
	return imagePickerNativeRow{}, false
}
