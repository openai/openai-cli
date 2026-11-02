package images

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
)

// Keep the action strip, editor, and shortcuts stationary as images arrive.
const imageStudioBodyY = 2

type imageStudioGeometry struct {
	frameWidth, editorWidth, editorRows, previewX, previewWidth, previewRows int
}

func imageStudioLayout(width, height int) (imageStudioGeometry, bool) {
	if width < 110 || height < 28 {
		return imageStudioGeometry{}, false
	}
	// Keep the inline workspace at a reading width. The expanded viewer owns the terminal.
	width = min(width, 100)
	editor := 44
	x := 4 + editor + 1 + 3 // editor, gutter, divider and two-cell inset
	return imageStudioGeometry{frameWidth: width, editorWidth: editor, editorRows: 17, previewX: x,
		previewWidth: width - x - 4, previewRows: 16}, true
}

// Cached decorations never contain image payloads. Spinner ticks can reuse them.
type imageStudioChrome struct {
	prefixes                                                             []string
	editorLines                                                          int
	top, toolbar, suffix, footer, bottom, emptyLabel, command, promptArg string
}

func (w *imagePickerWorkspace) studioChrome(m *imagePicker, g imageStudioGeometry) *imageStudioChrome {
	if w.studio != nil {
		return w.studio
	}
	border := m.workspaceImageBorder("│")
	suffix := ansi.CursorHorizontalAbsolute(g.frameWidth-2) + border
	frameRow := func(text string) string { return "  " + border + " " + text + suffix }
	width := g.frameWidth - 8
	title := lipgloss.NewStyle().Bold(m.color).Render("Create image")
	toolbar := "  " + title + "  " + m.studioActions(width-ansi.StringWidth(title)-4)
	chrome := &imageStudioChrome{
		top: m.workspaceImageFrame("", g.frameWidth, true), toolbar: toolbar,
		suffix: suffix, footer: frameRow("  " + m.studioHints(g)),
		bottom:     m.workspaceImageFrame("", g.frameWidth, false),
		emptyLabel: m.workspaceText("Command preview", imageSecondary),
	}
	left := strings.Split(w.header.Content, "\n")
	chrome.editorLines = len(left)
	for i := range g.editorRows {
		line := ""
		if i < len(left) {
			line = left[i]
		}
		// Pad the entire editor up to the divider. Left-only edits can then
		// replace old text without erasing a native image on the same row.
		chrome.prefixes = append(chrome.prefixes, "  "+border+" "+cliterminal.FitPrompt(line, g.editorWidth+1)+ansi.CursorHorizontalAbsolute(g.previewX-2)+border+"  ")
	}
	// Show the executable and the changing prompt. The action strip opens every flag.
	if args := m.settings.args(); len(args) >= 2 {
		tail := cliterminal.FormatCommand(args[len(args)-2:], m.shell)
		if tail != "" {
			chrome.command = "openai images generate " + m.workspaceText("…", imageSecondary)
			arg := imagePickerLine(strings.TrimPrefix(tail, "openai "))
			if cells := ansi.StringWidth(arg); cells > g.previewWidth {
				const label = "--prompt …"
				arg = label + ansi.TruncateLeft(arg, cells-(g.previewWidth-ansi.StringWidth(label)), "")
			}
			chrome.promptArg = m.workspaceText(arg, imageSecondary)
		} else {
			chrome.command = m.workspaceText(imagePickerUnsupportedShell, imageSecondary)
		}
	}
	w.studio = chrome
	return chrome
}

func (w *imagePickerWorkspace) studioView(m *imagePicker, g imageStudioGeometry, busy bool) []string {
	c := w.studioChrome(m, g)
	w.ensureGallery(m, true)
	w.top, w.bodyStart = imageStudioBodyY, imageStudioBodyY
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
	lines := []string{top, m.workspaceImageRow(toolbar, g.frameWidth, false)}
	var right []string
	if w.viewportRows == 0 && m.focus != "command" {
		right = []string{c.emptyLabel, c.command}
		if c.promptArg != "" {
			right = append(right, c.promptArg)
		}
	}
	receipt := ""
	rightRows := max(w.viewportRows, len(right))
	if len(w.images) > 0 {
		receipt = w.savedLocation(m)
		rightRows++
	}
	focusRow := -1
	if m.focus == "images" && w.viewportRows > 0 {
		focusRow = 0
		if len(w.galleryStarts) > 0 {
			if caption := w.galleryStarts[w.selected] - w.offset; caption >= 0 && caption < w.viewportRows {
				focusRow = caption
			}
		}
	}
	for i := range max(c.editorLines, rightRows) {
		prefix := c.prefixes[i]
		if i == focusRow {
			prefix = strings.TrimSuffix(prefix, "  ") + m.workspaceText("› ", imageActive)
		}
		row := ""
		switch {
		case i < w.viewportRows:
			at := w.offset + i
			if native, ok := w.nativeDisplay[at]; ok {
				lines = append(lines, w.studioNativeRow(native, len(lines), prefix, c.suffix))
				continue
			}
			row = w.displayRows[at]
		case i < len(right):
			row = right[i]
		case i == w.viewportRows:
			row = receipt
		}
		lines = append(lines, prefix+ansi.Truncate(row, g.previewWidth, "…")+c.suffix)
	}
	return append(lines, c.footer, c.bottom)
}
