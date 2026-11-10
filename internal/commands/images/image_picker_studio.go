package images

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
)

// Keep the action strip, editor, and shortcuts stationary as images arrive.
const (
	imageStudioBodyY = 3
	studioPrevButton = " ‹  "
	studioNextButton = "  › "
	studioArrowWidth = 4
)

type imageStudioGeometry struct {
	frameWidth, editorWidth, editorRows, previewX, previewWidth, previewRows int
	editorX, bodyY                                                           int
}

func imageStudioLayout(width, height int) (imageStudioGeometry, bool) {
	if width < 110 || height < 28 {
		return imageStudioGeometry{}, false
	}
	// Keep the inline workspace at a reading width. The expanded viewer owns the terminal.
	width = min(width, 120)
	editor := 44
	x := 4 + editor + 1 + 3 // editor, gutter, divider and two-cell inset
	g := imageStudioGeometry{frameWidth: width, editorWidth: editor, editorRows: 17, previewX: x,
		previewWidth: width - x - 4, previewRows: 19, editorX: 4, bodyY: imageStudioBodyY}
	return g, true
}

func (g imageStudioGeometry) draftWidth() int {
	return g.editorWidth - 3
}

// Cached decorations never contain image payloads. Spinner ticks can reuse them.
type imageStudioChrome struct {
	scrollable                                    bool
	prefixes, suffixes                            []string
	editorLines                                   int
	top, toolbar, divider, suffix, footer, bottom string
}

func (w *imagePickerWorkspace) studioChrome(m *imagePicker, g imageStudioGeometry) *imageStudioChrome {
	scrollable := len(w.displayRows) > g.previewRows
	if w.studio != nil && w.studio.scrollable == scrollable {
		return w.studio
	}
	border := m.workspaceImageBorder("│")
	suffix := ansi.CursorHorizontalAbsolute(g.frameWidth-2) + border
	frameRow := func(text string) string { return "  " + border + " " + text + suffix }
	width := g.frameWidth - 8
	title := m.workspaceText("Create image", imageSecondary)
	toolbar := "  " + title + "  " + m.studioActions(width-ansi.StringWidth(title)-4)
	palette := m.palette()
	rule := cliterminal.SectionDivider(width, m.color && m.profile == colorprofile.TrueColor, palette.fill, palette.border, palette.secondary)
	if m.profile != colorprofile.TrueColor {
		rule = m.workspaceImageBorder(rule)
	}
	chrome := &imageStudioChrome{
		scrollable: scrollable,
		top:        m.workspaceImageFrame("", g.frameWidth, true), toolbar: toolbar, divider: frameRow(rule),
		suffix: suffix, footer: "    " + m.studioHints(g, len(w.images) > 1, scrollable),
		bottom: m.workspaceImageFrame("", g.frameWidth, false),
	}
	left := strings.Split(w.header.Content, "\n")
	chrome.editorLines = len(left)
	for i := range max(g.editorRows, g.previewRows+10) {
		line := ""
		if i < len(left) {
			line = left[i]
		}
		// Pad the entire editor up to the divider. Left-only edits can then
		// replace old text without erasing a native image on the same row.
		chrome.prefixes = append(chrome.prefixes, "  "+border+" "+cliterminal.FitPrompt(line, g.editorWidth+1)+ansi.CursorHorizontalAbsolute(g.previewX-2)+border+"  ")
		chrome.suffixes = append(chrome.suffixes, suffix)
	}
	w.studio = chrome
	return chrome
}

func (w *imagePickerWorkspace) studioView(m *imagePicker, g imageStudioGeometry, busy bool) []string {
	w.ensureGallery(m, true)
	c := w.studioChrome(m, g)
	w.top, w.bodyStart = g.bodyY, g.bodyY
	w.viewportRows = min(len(w.displayRows), g.previewRows)
	w.scroll(0)
	w.bodyEnd = w.bodyStart + w.viewportRows

	top, toolbar := c.top, c.toolbar
	if status := w.inlineStatus(m, busy); status != "" && w.longNotice == "" {
		space := g.frameWidth - 8 - ansi.StringWidth(toolbar+status)
		if space >= 2 {
			toolbar += strings.Repeat(" ", space) + status
		} else {
			// Keep the full notice above the controls. Longer notices are paged in the gallery.
			top = m.workspaceImageFrame(status, g.frameWidth, true)
		}
	}
	lines := []string{top, m.workspaceImageRow(toolbar, g.frameWidth, false), c.divider}
	var right []string
	if w.viewportRows == 0 && len(w.images) == 0 && m.focus != "command" {
		right = m.studioCanvas(g, c.editorLines)
	}
	var receipt []string
	captionRow := -1
	rightRows := max(w.viewportRows, len(right))
	if len(w.images) > 0 {
		receipt = w.studioReceipt(m, g.previewWidth)
		captionRow = w.galleryStart(w.selected) - w.offset
		rightRows += len(receipt)
	}
	focusRow := -1
	if m.focus == "images" {
		focusRow = 0
		if captionRow >= 0 && captionRow < w.viewportRows {
			focusRow = captionRow
		}
	}
	for i := range max(c.editorLines, rightRows) {
		prefix, suffix := c.prefixes[i], c.suffixes[i]
		if i == focusRow {
			marker := m.workspaceText("› ", imageActive)
			if i == captionRow {
				marker = m.studioImageHeaderStyle(m.palette().accent).Render("› ")
			}
			prefix = strings.TrimSuffix(prefix, "  ") + marker
		}
		row := ""
		switch {
		case i < w.viewportRows:
			at := w.offset + i
			if native, ok := w.nativeDisplay[at]; ok {
				lines = append(lines, w.studioNativeRow(native, len(lines), prefix, suffix))
				continue
			}
			row = w.displayRows[at]
			if i == captionRow {
				controls := w.studioPager(m)
				entry := w.images[w.selected]
				entry.protocol = "" // Keep this row for the filename, count and pager.
				nameWidth := g.previewWidth - ansi.StringWidth(controls)
				// Remove caption styling before applying one uninterrupted selection surface.
				filename := ansi.Strip(entry.caption(m, nameWidth-1))
				row = m.studioImageHeaderStyle(m.palette().selected).Width(nameWidth).Render(filename) + controls
			}
		case i < len(right):
			row = right[i]
		case i >= w.viewportRows && i-w.viewportRows < len(receipt):
			row = receipt[i-w.viewportRows]
		}
		lines = append(lines, prefix+ansi.Truncate(row, g.previewWidth, "…")+suffix)
	}
	return append(lines, c.bottom, c.footer)
}

// Stable button and count widths keep the painted pager and pointer targets aligned.
func studioPagerWidth(total int) int {
	return 2*studioArrowWidth + 2*len(fmt.Sprint(total)) + len(" / ")
}

func (w *imagePickerWorkspace) studioPager(m *imagePicker) string {
	palette := m.palette()
	countColor := palette.secondary
	if m.focus == "images" {
		countColor = palette.selected
	}
	count, arrow := m.studioImageHeaderStyle(countColor), m.studioImageHeaderStyle(palette.accent)
	previous, next := strings.Repeat(" ", studioArrowWidth), strings.Repeat(" ", studioArrowWidth)
	if !m.galleryModal() {
		if w.selected > 0 {
			previous = studioPrevButton
		}
		if w.selected+1 < len(w.images) {
			next = studioNextButton
		}
	}
	label := fmt.Sprintf("%*d / %d", len(fmt.Sprint(len(w.images))), w.selected+1, len(w.images))
	return arrow.Render(previous) + count.Render(label) + arrow.Render(next)
}

// Give the image the rows reclaimed from its former multi-line receipt.
func (w *imagePickerWorkspace) studioReceipt(m *imagePicker, width int) []string {
	entry := &w.images[w.selected]
	folder := filepath.Dir(entry.file.Path)
	if absolute, err := filepath.Abs(folder); err == nil {
		folder = absolute
	}
	path := imagePickerLine(imagePickerFolderLabel(folder))
	available := max(1, width-len("Saved to "))
	// Preserve the root and final folder instead of hiding the destination.
	if cells := ansi.StringWidth(path); cells > available {
		tail := ansi.TruncateLeft(path, cells-available/2, "")
		path = ansi.Truncate(path, available-ansi.StringWidth(tail)-1, "") + "…" + tail
	}
	return []string{m.workspaceText("Saved to ", imageSuccess) + m.workspaceText(path, imageSecondary)}
}

// Each header span shares the fill so foreground resets cannot interrupt it.
func (m *imagePicker) studioImageHeaderStyle(color string) lipgloss.Style {
	style := m.foreground(color)
	if m.focus == "images" && m.color && m.profile >= colorprofile.ANSI256 {
		style = style.Background(m.profile.Convert(lipgloss.Color(m.palette().fill)))
	}
	return style
}
