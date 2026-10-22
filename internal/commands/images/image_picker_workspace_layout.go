package images

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/terminalimage"
)

// Keep result selection separate from save receipts and request diagnostics.
func (w *imagePickerWorkspace) inlineView(m *imagePicker, busy bool) []string {
	header := strings.Split(w.header.Content, "\n")
	w.top, w.viewportRows, w.bodyStart, w.bodyEnd = len(header), 0, 0, 0
	if m.width < 40 || m.height < 12 {
		return header
	}
	// Controls belong to the form. Progress and results must not move them.
	captions := m.height-len(header)-3 > 1
	if w.layoutWidth != m.width || w.layoutCaptions != captions || w.layoutCount != len(w.rows) || w.layoutSelected != w.selected || w.layoutRevision != w.galleryRevision || w.galleryJump || w.galleryDetails {
		w.layoutCaptions = captions
		w.layoutGallery(m)
	}
	status := ""
	if busy {
		status = w.progress()
	} else if m.note != "" && m.note != "All images saved." && (m.note != imagePickerRequestFailureNote || len(w.rows) == 0) {
		// New form notices take precedence over retained request or image-open errors.
		status = imagePickerLine(m.note)
	} else if w.openError && w.openErrorIndex == w.selected {
		status = "Could not open this image."
	} else if m.note == imagePickerRequestFailureNote && w.offset >= w.messageRows {
		status = "Request failed."
	}
	lines := append([]string(nil), header...)
	if status != "" || len(w.displayRows) > 0 {
		lines = append(lines, "    "+ansi.Truncate(status, max(0, m.width-6), "…"))
	}
	if len(w.displayRows) > 0 {
		frameRows := 0
		if len(w.images) > 0 {
			frameRows = 2
		}
		w.viewportRows = min(len(w.displayRows), max(0, m.height-len(lines)-frameRows))
		w.scroll(0)
		title, location := "Images", ""
		if len(w.images) > 0 {
			if !w.layoutCaptions {
				title = w.imageTitle()
			}
			folder := filepath.Dir(w.images[w.selected].file.Path)
			label := imagePickerFolderLabel(folder)
			if label != "~" {
				label = filepath.Base(folder)
			}
			location = m.workspaceImageBorder("Saved in " + imagePickerLine(label))
		}
		if len(w.images) > 0 {
			lines = append(lines, m.workspaceImageFrame(title, m.width, true))
		}
		w.bodyStart = len(lines)
		available := min(w.viewportRows, len(w.displayRows)-w.offset)
		if len(w.images) > 0 {
			available = w.viewportRows
		}
		for i := 0; i < available; i++ {
			row := ""
			if w.offset+i < len(w.displayRows) {
				row = w.displayRows[w.offset+i]
				if native, ok := w.nativeDisplay[w.offset+i]; ok {
					row = w.nativeViewRow(native, len(lines))
				}
			}
			if !strings.Contains(row, "\x1b_G") && !strings.Contains(row, "\x1b]1337;") {
				row = ansi.Truncate(row, max(0, m.width-8), "…")
			}
			if len(w.images) > 0 {
				lines = append(lines, m.workspaceImageRow(row, m.width))
			} else {
				lines = append(lines, "    "+row)
			}
		}
		w.bodyEnd = len(lines)
		if len(w.images) > 0 {
			lines = append(lines, m.workspaceImageFrame(location, m.width, false))
		}
	}
	return lines
}

func (w *imagePickerWorkspace) imageTitle() string {
	title := fmt.Sprintf("Image %d · %s", w.selected+1, imagePickerLine(filepath.Base(w.images[w.selected].file.Path)))
	if w.images[w.selected].protocol == "blocks" {
		title += " · Color preview"
	}
	return title
}

// Native slots retain their logical geometry without retaining PNG payloads.
func (w *imagePickerWorkspace) layoutGallery(m *imagePicker) {
	anchor, relative := -1, 0
	if len(w.galleryStarts) > 0 && w.offset >= w.galleryStarts[0] {
		anchor = w.galleryImageAt(w.offset)
		relative = w.offset - w.galleryStarts[anchor]
	}
	first := w.layoutWidth == 0
	w.displayRows = nil
	w.nativeDisplay = make(map[int]imagePickerNativeRow)
	appendRow := func(row string) {
		if strings.Contains(row, "\x1b_G") || strings.Contains(row, "\x1b]1337;") {
			if cells := terminalimage.RowColumns(row); cells < 0 || cells > max(1, m.width-8) {
				row = ""
			}
			w.displayRows = append(w.displayRows, row)
		} else {
			w.displayRows = append(w.displayRows, strings.Split(ansi.Wrap(row, max(1, m.width-8), ""), "\n")...)
		}
	}
	for _, row := range w.rows {
		appendRow(row)
	}
	w.messageRows = len(w.displayRows)
	w.galleryStarts = make([]int, len(w.images))
	for i, entry := range w.images {
		if i == 0 && w.messageRows > 0 {
			appendRow("")
		}
		w.galleryStarts[i] = len(w.displayRows)
		marker := "  "
		if i == w.selected {
			marker = "› "
		}
		caption := fmt.Sprintf("%sImage %d · %s", marker, i+1, imagePickerLine(filepath.Base(entry.file.Path)))
		if entry.protocol == "blocks" {
			caption += " · Color preview"
		}
		// Captions occupy one row; paths cannot change image geometry or anchors.
		if w.layoutCaptions {
			w.displayRows = append(w.displayRows, ansi.Truncate(caption, max(1, m.width-8), "…"))
		}
		switch {
		case entry.rowCount() == 0:
			appendRow("Preview unavailable. Press O to open the saved image.")
		case entry.columns > max(1, m.width-8):
			appendRow("Select this image to fit the preview. Press O to open it.")
		case entry.nativeRows > 0:
			for row := range entry.nativeRows {
				w.nativeDisplay[len(w.displayRows)] = imagePickerNativeRow{i, row, entry.columns, entry.nativeRows, false}
				w.displayRows = append(w.displayRows, "")
			}
		default:
			for _, row := range entry.rows {
				appendRow(row)
			}
		}
	}
	switch {
	case w.galleryDetails:
		w.offset = 0
	case len(w.images) > 0 && (first || w.galleryJump):
		w.offset = w.galleryOffset(w.selected, w.images[w.selected].inlineOffset)
	case anchor >= 0 && anchor < len(w.galleryStarts):
		w.offset = w.galleryOffset(anchor, relative)
	}
	w.galleryJump, w.galleryDetails = false, false
	w.layoutWidth, w.layoutCount, w.layoutSelected, w.layoutRevision = m.width, len(w.rows), w.selected, w.galleryRevision
}

func (w *imagePickerWorkspace) galleryImageAt(offset int) int {
	index := 0
	for i, start := range w.galleryStarts {
		if start > offset {
			break
		}
		index = i
	}
	return index
}

func (w *imagePickerWorkspace) galleryOffset(index, relative int) int {
	end := len(w.displayRows)
	if index+1 < len(w.galleryStarts) {
		end = w.galleryStarts[index+1]
	}
	start := w.galleryStarts[index]
	return start + max(0, min(relative, max(0, end-start-1)))
}

func (w *imagePickerWorkspace) clearRequestDiagnostics() {
	if len(w.rows) > 0 && len(w.galleryStarts) > 0 && w.offset < w.galleryStarts[0] {
		w.galleryJump = true
	}
	w.rows = nil
}
