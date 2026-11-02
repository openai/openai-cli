package images

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
)

// Keep the action strip, editor, and shortcuts stationary as images arrive.
const imageStudioBodyY = 5

type imageStudioGeometry struct {
	editorWidth, editorRows, previewX, previewWidth, previewRows int
}

func imageStudioLayout(width, height int) (imageStudioGeometry, bool) {
	if width < 110 || height < 28 {
		return imageStudioGeometry{}, false
	}
	editor := min(70, (width-10)*45/100)
	x := 4 + editor + 1 + 3 // editor, gutter, divider and two-cell inset
	return imageStudioGeometry{editorWidth: editor, editorRows: 17, previewX: x,
		previewWidth: width - x - 4, previewRows: 16}, true
}

// Cached decorations never contain image payloads. Spinner ticks can reuse them.
type imageStudioChrome struct {
	top, prefixes                                          []string
	suffix, footer, bottom, emptyLabel, command, promptArg string
}

func (w *imagePickerWorkspace) studioChrome(m *imagePicker, g imageStudioGeometry) *imageStudioChrome {
	if w.studio != nil {
		return w.studio
	}
	border := m.workspaceImageBorder("│")
	suffix := ansi.CursorHorizontalAbsolute(m.width-2) + border
	frameRow := func(text string) string { return "  " + border + " " + text + suffix }
	width := m.width - 8
	title := lipgloss.NewStyle().Bold(m.color).Render("Create image")
	badge := m.workspaceText("Image studio", imageSecondary)
	chrome := &imageStudioChrome{
		top: []string{m.workspaceImageFrame("", m.width, true),
			frameRow("  " + title + strings.Repeat(" ", width-2-ansi.StringWidth(title+badge)) + badge),
			frameRow(m.studioActions(width)),
			frameRow("  " + m.workspaceImageBorder(strings.Repeat("─", width-2))),
			frameRow("")},
		suffix: suffix, footer: frameRow("  " + m.studioHints(g)),
		bottom:     m.workspaceImageFrame("", m.width, false),
		emptyLabel: m.workspaceText("Command preview", imageSecondary),
	}
	left := strings.Split(w.header.Content, "\n")
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
	w.top = imageStudioBodyY
	w.viewportRows = min(len(w.displayRows), g.previewRows)
	w.scroll(0)
	w.bodyStart = imageStudioBodyY + 1
	w.bodyEnd = w.bodyStart + w.viewportRows
	status := w.inlineStatus(m, busy)
	if status == "" && len(w.images) == 0 && len(w.displayRows) == 0 {
		status = m.workspaceText("○ Idle", imageSecondary)
	}
	title := lipgloss.NewStyle().Bold(m.color).Render("Preview")
	captionFocus := m.focus == "images" && len(w.images) > 1 && w.galleryStarts[w.selected] >= w.offset && w.galleryStarts[w.selected] < w.offset+w.viewportRows
	if m.focus == "images" && !captionFocus {
		title = m.workspaceText("› ", imageActive) + title
	}
	lines := append([]string(nil), c.top...)
	if w.longNotice != "" {
		status = "" // The scrollable gallery contains the full notice.
	} else if ansi.StringWidth(status)+ansi.StringWidth(title)+1 > g.previewWidth {
		lines[imageStudioBodyY-1] = m.workspaceImageRow("  "+status, m.width, false)
		status = ""
	}
	heading := title + strings.Repeat(" ", max(1, g.previewWidth-ansi.StringWidth(title+status))) + status
	for i, prefix := range c.prefixes {
		row := ""
		at := w.offset + i - 1
		switch {
		case i == 0:
			row = heading
		case i <= w.viewportRows:
			if native, ok := w.nativeDisplay[at]; ok {
				lines = append(lines, w.studioNativeRow(native, len(lines), prefix, c.suffix))
				continue
			}
			row = w.displayRows[at]
			if captionFocus && at == w.galleryStarts[w.selected] {
				prefix = strings.TrimSuffix(prefix, "  ") + m.workspaceText("› ", imageActive)
			}
		case len(w.displayRows) == 0 && m.focus == "command" && i == 2:
			row = m.workspaceText("No images yet.", imageSecondary)
		case len(w.displayRows) == 0 && m.focus != "command":
			switch i {
			case 2:
				row = c.emptyLabel
			case 3:
				row = c.command
			case 4:
				row = c.promptArg
			}
		}
		lines = append(lines, prefix+ansi.Truncate(row, g.previewWidth, "…")+c.suffix)
	}
	receipt := ""
	if len(w.images) > 0 {
		receipt = w.savedLocation(m)
	}
	lines = append(lines, "  "+m.workspaceImageBorder("│")+ansi.CursorHorizontalAbsolute(g.previewX+1)+ansi.Truncate(receipt, g.previewWidth, "…")+c.suffix, c.footer, c.bottom)
	return lines
}
