package images

import (
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/terminalimage"
)

// Keep result selection separate from save receipts and request diagnostics.
func (w *imagePickerWorkspace) inlineView(m *imagePicker, busy bool) []string {
	if g, ok := imageStudioLayout(m.width, m.height); ok {
		return w.studioView(m, g, busy)
	}
	header := strings.Split(w.header.Content, "\n")
	w.top, w.viewportRows, w.bodyStart, w.bodyEnd = len(header), 0, 0, 0
	if m.width < 40 || m.height < 12 {
		return header
	}
	// Controls belong to the form. Progress and results must not move them.
	captions := m.height-len(header)-3 > 1
	w.ensureGallery(m, captions)
	status := w.inlineStatus(m, busy)
	lines := append([]string(nil), header...)
	// A gallery keeps its progress slot. Diagnostics alone start directly below the form.
	if status != "" || len(w.images) > 0 {
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
		focusRow := -1
		if len(w.images) > 0 {
			if !w.layoutCaptions {
				titleWidth := m.width - 9 // Frame corners, insets and label spacing.
				if m.focus == "images" {
					titleWidth -= 2 // The focus marker precedes the compact title.
				}
				title = w.images[w.selected].caption(m, titleWidth)
			}
			location = w.savedLocation(m)
			if m.focus == "images" {
				start := w.galleryStarts[w.selected]
				if len(w.images) > 1 && w.layoutCaptions && start >= w.offset && start < w.offset+w.viewportRows {
					focusRow = start
				} else {
					title = m.workspaceText("›", imageActive) + " " + title
				}
			}
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
				lines = append(lines, m.workspaceImageRow(row, m.width, w.offset+i == focusRow))
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

func (w *imagePickerWorkspace) savedLocation(m *imagePicker) string {
	folder := filepath.Dir(w.images[w.selected].file.Path)
	label := imagePickerFolderLabel(folder)
	if label != "~" {
		label = filepath.Base(folder)
	}
	return m.workspaceSavedLocation(label)
}

func (w *imagePickerWorkspace) ensureGallery(m *imagePicker, captions bool) {
	width := imagePickerInlinePreviewWidth(interactiveImageSize{width: m.width, height: m.height})
	// A path error can exceed even the full-width notice line. Keep every
	// character reachable with the same paging controls as request details.
	notice := ""
	if _, wide := imageStudioLayout(m.width, m.height); wide && !m.generating && len(m.note) > 0 {
		if text := imagePickerLine(m.note); ansi.StringWidth(text) > m.width-10 {
			notice = text
		}
	}
	if notice != w.longNotice || notice != "" && m.noteTone != w.longNoticeTone {
		if notice != "" {
			w.galleryDetails = true
		} else if w.offset < w.messageRows {
			w.galleryJump = true
		}
		w.longNotice, w.longNoticeTone = notice, m.noteTone
		w.galleryRevision++
	}
	if w.layoutWidth != width || w.layoutCaptions != captions || w.layoutCount != len(w.rows) || w.layoutSelected != w.selected || w.layoutRevision != w.galleryRevision || w.galleryJump || w.galleryDetails {
		w.layoutCaptions = captions
		w.layoutGallery(m)
	}
}

func (w *imagePickerWorkspace) inlineStatus(m *imagePicker, busy bool) string {
	status := ""
	if busy {
		status = w.progress(m)
	} else if m.note != "" && m.note != "All images saved." && (m.note != imagePickerRequestFailureNote || len(w.rows) == 0) {
		// New form notices take precedence over retained request or image-open errors.
		role := m.noteTone
		if m.note == imagePickerRequestFailureNote {
			role = imageFailure
		}
		status = m.workspaceText(imagePickerLine(m.note), role)
	} else if w.openError && w.openErrorIndex == w.selected {
		status = m.workspaceText("Could not open this image.", imageFailure)
	} else if m.note == imagePickerRequestFailureNote && w.offset >= w.messageRows {
		status = m.workspaceText(w.requestFailureSummary(), imageFailure)
	}
	return status
}

func (w *imagePickerWorkspace) requestFailureSummary() string {
	if w.requestFailure != "" {
		return w.requestFailure
	}
	return imagePickerRequestFailureNote
}

// Native slots retain their logical geometry without retaining PNG payloads.
func (w *imagePickerWorkspace) layoutGallery(m *imagePicker) {
	width := imagePickerInlinePreviewWidth(interactiveImageSize{width: m.width, height: m.height})
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
			if cells := terminalimage.RowColumns(row); cells < 0 || cells > width {
				row = ""
			}
			w.displayRows = append(w.displayRows, row)
		} else {
			w.displayRows = append(w.displayRows, strings.Split(ansi.Wrap(row, width, ""), "\n")...)
		}
	}
	if w.longNotice != "" {
		appendRow(m.workspaceText(w.longNotice, w.longNoticeTone))
	}
	for i, row := range w.rows {
		appendRow(m.workspaceText(row, w.detailTones[i]))
	}
	w.messageRows = len(w.displayRows)
	w.galleryStarts = make([]int, len(w.images))
	for i, entry := range w.images {
		if i == 0 && w.messageRows > 0 {
			appendRow("")
		}
		w.galleryStarts[i] = len(w.displayRows)
		// Captions occupy one row; paths cannot change image geometry or anchors.
		if w.layoutCaptions {
			w.displayRows = append(w.displayRows, entry.caption(m, width))
		}
		switch {
		case entry.rowCount() == 0:
			appendRow("Preview unavailable. Press O to open the saved image.")
		case entry.columns > width:
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
	w.layoutWidth, w.layoutCount, w.layoutSelected, w.layoutRevision = width, len(w.rows), w.selected, w.galleryRevision
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
	w.detailTones = nil
	w.requestFailure = ""
}
