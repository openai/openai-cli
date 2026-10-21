package images

import (
	"context"
	"errors"
	"fmt"
	"image"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/imageoutput"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"github.com/openai/openai-cli/internal/terminalimage"
)

type imagePickerWorkspaceImage struct {
	file                        imageoutput.SavedImage
	protocol                    string
	rows                        []string
	columns                     int
	start, displayStart, offset int
}

type imagePickerExpandedRows struct {
	revision uint64
	index    int
	size     interactiveImageSize
	rows     []string
	protocol string
	inline   bool
	columns  int
}

// FontError guarantees that no image glyphs were written and rollback succeeded.
// Match saved-image display: color fallback never replaces existing font glyphs.
func imagePickerPreviewRows(ctx context.Context, out *os.File, img image.Image, protocol string, columns int) ([]string, string, error) {
	rows, err := terminalimage.PrepareRows(ctx, out, img, protocol, columns)
	var fontErr *terminalimage.FontError
	if ctx.Err() != nil {
		return nil, protocol, errors.Join(err, ctx.Err())
	}
	if protocol != "font" || !errors.As(err, &fontErr) || !imagePickerSafePreviewFailure(err) || !savedImageBlockColor(os.Getenv) {
		return rows, protocol, err
	}
	rows, err = terminalimage.PrepareRows(ctx, out, img, "blocks", columns)
	if err != nil {
		return nil, protocol, err
	}
	return rows, "blocks", nil
}

func (m *imagePicker) workspaceKeys(keys [][2]string, width int) string {
	key, muted := lipgloss.NewStyle().Bold(true), lipgloss.NewStyle()
	if m.color {
		accent, secondary := "#3159BC", "#657087"
		if m.dark {
			accent, secondary = "#8AA8FF", "#A4ACC2"
		}
		key = key.Foreground(lipgloss.Color(accent))
		muted = muted.Foreground(lipgloss.Color(secondary))
	}
	return imagePickerShortcutLine(keys, width, key, muted)
}

func (w *imagePickerWorkspace) expandedView(m *imagePicker, busy bool) []string {
	if m.width < 40 || m.height < 12 {
		return []string{"  Enlarge the terminal.", "    " + m.workspaceKeys([][2]string{{"Esc", "Back"}}, max(0, m.width-6)), "    " + m.workspaceKeys([][2]string{{"Ctrl+C", "Quit"}}, max(0, m.width-6))}[:min(3, max(0, m.height))]
	}
	lines := make([]string, max(1, m.height))
	entry := &w.images[w.selected]
	title := fmt.Sprintf("Images · %d of %d", w.selected+1, len(w.images))
	if entry.protocol == "blocks" {
		title += " · Color preview"
	}
	lines[0] = "    " + ansi.Truncate(title, max(0, m.width-6), "…")
	if len(lines) > 1 {
		lines[1] = "    " + ansi.Truncate(imagePickerLine(imagePickerFolderLabel(entry.file.Path)), max(0, m.width-6), "…")
	}
	if len(lines) > 3 {
		if busy {
			lines[2] = "    " + ansi.Truncate(w.progress(), max(0, m.width-6), "…")
		} else if m.note != "" {
			lines[2] = "    " + ansi.Truncate(imagePickerLine(m.note), max(0, m.width-6), "…")
		}
		rows := w.expandedRows
		if w.expandedIndex != w.selected || w.expandedSize.width != m.width || w.expandedSize.height != m.height || len(rows) == 0 {
			rows = entry.rows
		}
		if len(rows) == 0 {
			rows = []string{"Preview disabled or unavailable. The saved file is unchanged."}
		}
		available := max(0, len(lines)-4)
		offset := max(0, min(entry.offset, max(0, len(rows)-available)))
		for i := 0; i < available && offset+i < len(rows); i++ {
			row := rows[offset+i]
			if cells := terminalimage.RowColumns(row); cells < 0 || cells > max(1, m.width-6) {
				row = ""
			}
			lines[i+3] = "    " + row
		}
	}
	quit := "Quit"
	if busy {
		quit = "Cancel"
	}
	lines[len(lines)-1] = "    " + m.workspaceKeys([][2]string{{"Esc", "Back"}, {"Ctrl+C", quit}, {"←→", "Images"}, {"↑↓", "Scroll"}}, max(0, m.width-6))
	return lines
}

// Decode and encode outside the input loop. Only one expanded frame is cached.
func (p *imagePickerInline) prepareExpanded() {
	w := p.workspace
	if len(w.images) == 0 {
		return
	}
	if !w.expanded && (w.images[w.selected].columns <= max(1, p.model.width-8) || len(w.images[w.selected].rows) == 0) {
		return
	}
	size, err := interactiveImageViewport(w.out)
	if err != nil {
		p.err = err
		p.output.cancel()
		return
	}
	expanded := w.expanded
	entry := w.images[w.selected]
	if !expanded && (entry.columns <= max(1, size.width-8) || len(entry.rows) == 0) {
		return
	}
	if w.previewPending && w.previewIndex == w.selected && w.previewSize == size && w.previewExpanded == expanded {
		return
	}
	if expanded && w.expandedIndex == w.selected && w.expandedSize == size && len(w.expandedRows) > 0 {
		return
	}
	if w.previewCancel != nil {
		w.previewCancel()
	}
	w.previewRevision++
	revision, index := w.previewRevision, w.selected
	w.previewPending, w.previewIndex, w.previewSize, w.previewExpanded = true, index, size, expanded
	parent := w.context
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	w.previewCancel = cancel
	w.previewWorkers.Add(1)
	prepare := w.prepareRows
	if prepare == nil {
		prepare = imagePickerPreviewRows
	}
	go func() {
		rows, protocol, preparedColumns, failure := imagePickerPrepareResize(ctx, w.out, entry, size, expanded, prepare)
		if failure != nil {
			w.previewErrorMu.Lock()
			w.previewError = errors.Join(w.previewError, failure)
			w.previewErrorMu.Unlock()
		}
		w.previewWorkers.Done()
		if failure != nil {
			w.send(imagePickerPreviewFailure{failure})
			return
		}
		if ctx.Err() == nil {
			w.send(imagePickerExpandedRows{revision: revision, index: index, size: size, rows: rows, protocol: protocol, inline: !expanded, columns: preparedColumns})
		}
	}()
}

func (p *imagePickerInline) expandImages() {
	w := p.workspace
	if len(w.images) == 0 {
		return
	}
	w.expanded = true
	p.prepareExpanded()
}

func (p *imagePickerInline) closeExpanded() {
	w := p.workspace
	w.expanded = false
	w.previewPending = false
	if w.previewCancel != nil {
		w.previewCancel()
		w.previewCancel = nil
	}
	w.previewRevision++
	p.submitAfter = time.Now().Add(imagePickerResumeQuiet)
	w.viewAfter = p.submitAfter
}

func (w *imagePickerWorkspace) selectImage(delta int) {
	if len(w.images) == 0 {
		return
	}
	w.selected = max(0, min(w.selected+delta, len(w.images)-1))
	if !w.expanded {
		w.offset = w.images[w.selected].displayStart
		w.scroll(0)
	}
}

func (w *imagePickerWorkspace) scrollExpanded(delta int) {
	if len(w.images) > 0 {
		rows := w.expandedRows
		if w.expandedIndex != w.selected || len(rows) == 0 {
			rows = w.images[w.selected].rows
		}
		limit := max(0, len(rows)-max(0, w.height-4))
		offset := max(0, min(w.images[w.selected].offset, limit))
		if delta >= limit {
			offset = limit
		} else if delta <= -limit {
			offset = 0
		} else {
			offset = max(0, min(offset+delta, limit))
		}
		w.images[w.selected].offset = offset
	}
}

func (w *imagePickerWorkspace) appendImage(entry imagePickerWorkspaceImage) {
	if len(w.images) > 0 {
		w.rows = append(w.rows, "")
	}
	entry.start = len(w.rows)
	w.rows = append(w.rows, fmt.Sprintf("Image %d · %s", len(w.images)+1, imagePickerLine(filepath.Base(entry.file.Path))))
	w.rows = append(w.rows, "")
	w.rows = append(w.rows, entry.rows...)
	w.images = append(w.images, entry)
}

// Reserve the shared four-cell inset and two-cell right margin before encoding.
func imagePickerPreviewColumns(bounds image.Rectangle, width, height, cw, ch int) int {
	return savedImagePreviewColumns(bounds, width-7, height, cw, ch)
}

// The outline owns its cells; native graphics keep the existing content origin.
func (m *imagePicker) workspaceImageBorder(text string) string {
	style := lipgloss.NewStyle()
	if m.color {
		color := "#BAC4D8"
		if m.dark {
			color = "#51566B"
		}
		style = style.Foreground(lipgloss.Color(color))
	}
	return style.Render(text)
}

func (m *imagePicker) workspaceImageFrame(label string, width int, top bool) string {
	left, right := "╰", "╯"
	if top {
		left, right = "╭", "╮"
	}
	inside := max(0, width-6)
	label = ansi.Truncate(label, max(0, inside-3), "…")
	if label == "" {
		return "  " + m.workspaceImageBorder(left+strings.Repeat("─", inside)+right)
	}
	return "  " + m.workspaceImageBorder(left+"─ ") + label + m.workspaceImageBorder(" "+strings.Repeat("─", max(0, inside-3-ansi.StringWidth(label)))+right)
}

func (m *imagePicker) workspaceImageRow(row string, width int) string {
	return "  " + m.workspaceImageBorder("│") + " " + row + ansi.CursorHorizontalAbsolute(max(1, width-2)) + m.workspaceImageBorder("│")
}

type imagePickerOpened struct {
	err   error
	index int
}

// Open only an explicitly selected saved image, using the filtered desktop helper.
func (p *imagePickerInline) openSavedImage() tea.Cmd {
	w := p.workspace
	if w.opening || len(w.images) == 0 {
		return nil
	}
	index := w.selected
	entry := w.images[index]
	parent := w.context
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	w.opening, w.openCancel = true, cancel
	open := w.openImage
	if open == nil {
		open = cliterminal.OpenBrowser
	}
	w.openWorkers.Add(1)
	go func() {
		err := func() error {
			defer cancel()
			if _, err := terminalimage.ReadSavedMatching(ctx, entry.file.Path, entry.file.SHA256); err != nil {
				return err
			}
			path, err := filepath.Abs(entry.file.Path)
			if err != nil {
				return err
			}
			return open(ctx, imagePickerFileURL(path, runtime.GOOS == "windows"))
		}()
		w.openWorkers.Done()
		w.send(imagePickerOpened{err: err, index: index})
	}()
	return nil
}

func imagePickerFileURL(path string, windows bool) string {
	address := url.URL{Scheme: "file"}
	if windows {
		path = strings.ReplaceAll(path, "\\", "/")
		if strings.HasPrefix(path, "//") {
			host, rest, _ := strings.Cut(strings.TrimPrefix(path, "//"), "/")
			address.Host, path = host, "/"+rest
		} else if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
	}
	address.Path = path
	return address.String()
}

type imagePickerPreviewFailure struct{ err error }

// Only FontError certifies successful rollback. Cancellation cannot hide another cause.
func imagePickerSafePreviewFailure(err error) bool {
	if err == nil {
		return true
	}
	if _, ok := err.(*terminalimage.FontError); ok {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, cause := range joined.Unwrap() {
			if !imagePickerSafePreviewFailure(cause) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return imagePickerSafePreviewFailure(wrapped.Unwrap())
	}
	return err == context.Canceled || err == context.DeadlineExceeded
}

func (w *imagePickerWorkspace) replaceImageRows(index int, rows []string, columns int, protocol string) {
	entry := &w.images[index]
	start, end := entry.start+2, entry.start+2+len(entry.rows)
	updated := append([]string(nil), w.rows[:start]...)
	updated = append(updated, rows...)
	w.rows = append(updated, w.rows[end:]...)
	delta := len(rows) - len(entry.rows)
	for i := index + 1; i < len(w.images); i++ {
		w.images[i].start += delta
	}
	entry.rows, entry.columns, entry.protocol = rows, columns, protocol
	w.layoutCount = -1
}

func imagePickerPrepareResize(ctx context.Context, out *os.File, entry imagePickerWorkspaceImage, size interactiveImageSize, expanded bool, prepare func(context.Context, *os.File, image.Image, string, int) ([]string, string, error)) ([]string, string, int, error) {
	rows := []string{"Preview unavailable. Open the saved file; no need to generate again."}
	protocol, preparedColumns := entry.protocol, 0
	if entry.protocol == "" {
		rows = []string{"Previews are disabled. The image is saved; open the saved file to view it."}
	} else if img, err := terminalimage.ReadSavedMatching(ctx, entry.file.Path, entry.file.SHA256); err == nil {
		viewport := size
		viewport.width -= 5 // The shared helper also reserves its final column.
		viewport.height--   // The picker adds a file caption above the viewer.
		columns := interactiveImageColumns(img, viewport)
		if !expanded {
			form := imagePicker{height: size.height, workspace: true}
			columns = imagePickerPreviewColumns(img.Bounds(), size.width, max(3, size.height-form.viewHeight()-2), size.cellWidth, size.cellHeight)
		}
		if entry.protocol == "font" {
			columns = terminalimage.FontPreviewColumns(img.Bounds(), columns, max(1, size.height-4))
		}
		if columns > 0 {
			if prepared, actual, err := prepare(ctx, out, img, entry.protocol, columns); err == nil {
				rows, protocol, preparedColumns = prepared, actual, columns
			} else if !imagePickerSafePreviewFailure(err) {
				return nil, protocol, 0, err
			}
		} else {
			rows = []string{"Enlarge the terminal to view this image. Escape returns to the form."}
		}
	}

	return rows, protocol, preparedColumns, nil
}
