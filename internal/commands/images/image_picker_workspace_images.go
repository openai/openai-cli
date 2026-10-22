package images

import (
	"context"
	"errors"
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
	"github.com/openai/openai-cli/internal/imagegallery"
	"github.com/openai/openai-cli/internal/imageoutput"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"github.com/openai/openai-cli/internal/terminalimage"
)

type imagePickerWorkspaceImage struct {
	file                 imageoutput.SavedImage
	protocol             string
	rows                 []string
	nativeRows           int
	columns              int
	offset, inlineOffset int
	unavailableSize      *interactiveImageSize
}

type imagePickerExpandedRows struct {
	revision            uint64
	index               int
	size                interactiveImageSize
	rows                []string
	protocol            string
	inline              bool
	columns             int
	viewportUnavailable bool
}

// FontError guarantees that no image glyphs were written and rollback succeeded.
// Match saved-image display: color fallback never replaces existing font glyphs.
func imagePickerPreviewRows(ctx context.Context, out *os.File, img image.Image, protocol string, columns int) ([]string, string, error) {
	rows, err := terminalimage.PrepareRows(ctx, out, img, protocol, columns)
	var fontErr *terminalimage.FontError
	if ctx.Err() != nil {
		return nil, protocol, errors.Join(err, ctx.Err())
	}
	if errors.Is(err, imagegallery.ErrFull) || protocol != "font" || !errors.As(err, &fontErr) || !imagePickerSafePreviewFailure(err) || !savedImageBlockColor(os.Getenv) {
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
	return imagePickerWorkspaceShortcutLine(keys, width, key, muted)
}

func (w *imagePickerWorkspace) expandedView(m *imagePicker, busy bool) []string {
	if m.width < 40 || m.height < 12 {
		return []string{"  Enlarge the terminal.", "    " + m.workspaceKeys([][2]string{{"Esc", "Back"}}, max(0, m.width-6)), "    " + m.workspaceKeys([][2]string{{"Ctrl+C", "Quit"}}, max(0, m.width-6))}[:min(3, max(0, m.height))]
	}
	lines := make([]string, max(1, m.height))
	entry := &w.images[w.selected]
	title := w.imageTitle()
	lines[0] = "    " + ansi.Truncate(title, max(0, m.width-6), "…")
	if len(lines) > 1 {
		lines[1] = "    " + ansi.Truncate(imagePickerLine("Saved in "+imagePickerFolderLabel(filepath.Dir(entry.file.Path))), max(0, m.width-6), "…")
	}
	if len(lines) > 3 {
		if busy {
			lines[2] = "    " + ansi.Truncate(w.progress(), max(0, m.width-6), "…")
		} else if m.note != "" {
			lines[2] = "    " + ansi.Truncate(imagePickerLine(m.note), max(0, m.width-6), "…")
		}
		rows, count, columns, native, expanded := w.expandedRows, w.expandedRowCount(), w.expandedColumns, w.expandedNativeRows > 0, true
		if w.expandedIndex != w.selected || w.expandedSize.width != m.width || w.expandedSize.height != m.height || count == 0 {
			rows = entry.rows
			count, columns, native, expanded = entry.rowCount(), entry.columns, entry.nativeRows > 0, false
		}
		if count == 0 {
			rows = []string{"Preview disabled or unavailable. The saved file is unchanged."}
			count = len(rows)
		}
		available := max(0, len(lines)-4)
		offset := max(0, min(entry.offset, max(0, count-available)))
		for i := 0; i < available && offset+i < count; i++ {
			row := ""
			if native {
				if columns <= max(1, m.width-6) {
					row = w.nativeViewRow(imagePickerNativeRow{w.selected, offset + i, columns, count, expanded}, i+3)
				}
			} else {
				row = rows[offset+i]
			}
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

// All font preparation in this workspace shares one mutable per-tab gallery.
// Waiting must remain cancellable when a view changes or the request exits.
func (w *imagePickerWorkspace) acquireFontPreview(ctx context.Context) (func(), error) {
	w.fontPreviewOnce.Do(func() { w.fontPreviewGate = make(chan struct{}, 1) })
	select {
	case w.fontPreviewGate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-w.fontPreviewGate
			return nil, err
		}
		return func() { <-w.fontPreviewGate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Decode and encode outside the input loop. Only one expanded frame is cached.
func (p *imagePickerInline) prepareExpanded() {
	w := p.workspace
	if len(w.images) == 0 {
		return
	}
	expanded := w.expanded
	index := w.selected
	missing, cacheMiss := w.missingNativeRow()
	reload := cacheMiss && missing.expanded == expanded
	columns := 0
	if cacheMiss {
		index, columns = missing.image, missing.columns
	}
	if !expanded {
		if !cacheMiss {
			index = w.inlineResizeIndex(interactiveImageSize{width: p.model.width, height: p.model.height})
		}
		if index < 0 {
			return
		}
	}
	size, err := interactiveImageViewport(w.out)
	if err != nil {
		p.err = err
		p.output.cancel()
		return
	}
	if !expanded && !cacheMiss {
		index = w.inlineResizeIndex(size)
		if index < 0 {
			return
		}
	}
	entry := w.images[index]
	if w.previewPending && w.previewIndex == index && w.previewSize == size && w.previewExpanded == expanded && w.previewReload == reload && w.previewColumns == columns {
		return
	}
	if expanded && !cacheMiss && w.expandedIndex == w.selected && w.expandedSize == size && w.expandedRowCount() > 0 {
		return
	}
	if w.previewCancel != nil {
		w.previewCancel()
	}
	w.previewRevision++
	revision := w.previewRevision
	w.previewPending, w.previewIndex, w.previewSize, w.previewExpanded = true, index, size, expanded
	w.previewReload, w.previewColumns = reload, columns
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
		var releaseFont func()
		if entry.protocol == "font" {
			var err error
			releaseFont, err = w.acquireFontPreview(ctx)
			if err != nil {
				w.previewWorkers.Done()
				return
			}
		}
		var prepared imagePickerExpandedRows
		var failure error
		if reload {
			prepared, failure = imagePickerPrepareReload(ctx, w.out, entry, columns, prepare)
		} else {
			prepared, failure = imagePickerPrepareResize(ctx, w.out, entry, size, expanded, prepare)
		}
		if releaseFont != nil {
			releaseFont()
		}
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
			prepared.revision, prepared.index, prepared.size, prepared.inline = revision, index, size, !expanded
			w.send(prepared)
		}
	}()
}

// A cache miss reuses the displayed column width. PrepareRows still measures
// current cell geometry; its returned row count becomes the new layout geometry.
func imagePickerPrepareReload(ctx context.Context, out *os.File, entry imagePickerWorkspaceImage, columns int, prepare func(context.Context, *os.File, image.Image, string, int) ([]string, string, error)) (imagePickerExpandedRows, error) {
	result := imagePickerExpandedRows{protocol: entry.protocol, rows: []string{"Preview unavailable. Open the saved file; no need to generate again."}}
	if entry.protocol == "" {
		result.rows = []string{"Previews are disabled. The image is saved; open the saved file to view it."}
		return result, nil
	}
	img, err := terminalimage.ReadSavedMatching(ctx, entry.file.Path, entry.file.SHA256)
	if err != nil {
		return result, nil
	}
	rows, protocol, err := prepare(ctx, out, img, entry.protocol, columns)
	if err != nil {
		if imagePickerSafePreviewFailure(err) {
			return result, nil
		}
		return result, err
	}
	result.rows, result.protocol, result.columns = rows, protocol, columns
	return result, nil
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
	if w.selected != w.layoutSelected {
		w.galleryJump = true
	}
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
	selected := max(0, min(w.selected+delta, len(w.images)-1))
	if selected == w.selected {
		return
	}
	w.selected = selected
	if !w.expanded {
		w.galleryJump = true
	}
}

func (w *imagePickerWorkspace) scrollExpanded(delta int) {
	if len(w.images) > 0 {
		count := w.expandedRowCount()
		if w.expandedIndex != w.selected || count == 0 {
			count = w.images[w.selected].rowCount()
		}
		limit := max(0, count-max(0, w.height-4))
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
	if imagePickerNativeRows(entry.rows) {
		entry.nativeRows = len(entry.rows)
		w.cacheNativeArrival(len(w.images), entry.columns, entry.rows)
		entry.rows = nil
	}
	w.images = append(w.images, entry)
	w.galleryRevision++
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

func (w *imagePickerWorkspace) replaceImageRows(result imagePickerExpandedRows) {
	entry := &w.images[result.index]
	if result.viewportUnavailable {
		// Retain the cached preview and its scroll anchor until the viewport fits.
		entry.unavailableSize = &result.size
		return
	}
	entry.unavailableSize = nil
	entry.rows, entry.columns, entry.protocol = result.rows, result.columns, result.protocol
	entry.nativeRows = 0
	if imagePickerNativeRows(result.rows) {
		entry.nativeRows, entry.rows = len(result.rows), nil
	}
	w.discardNativeGeometry(result.index, false, entry.columns, entry.nativeRows)
	w.galleryRevision++
}

func imagePickerPrepareResize(ctx context.Context, out *os.File, entry imagePickerWorkspaceImage, size interactiveImageSize, expanded bool, prepare func(context.Context, *os.File, image.Image, string, int) ([]string, string, error)) (imagePickerExpandedRows, error) {
	result := imagePickerExpandedRows{protocol: entry.protocol, rows: []string{"Preview unavailable. Open the saved file; no need to generate again."}}
	if entry.protocol == "" {
		result.rows = []string{"Previews are disabled. The image is saved; open the saved file to view it."}
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
				result.rows, result.protocol, result.columns = prepared, actual, columns
			} else if !imagePickerSafePreviewFailure(err) {
				return result, err
			}
		} else {
			result.viewportUnavailable = true
			result.rows = []string{"Enlarge the terminal to view this image. Escape returns to the form."}
		}
	}

	return result, nil
}

// Resize visible oversized or viewport-blocked previews through the single worker.
func (w *imagePickerWorkspace) inlineResizeIndex(size interactiveImageSize) int {
	needs := func(i int) bool {
		entry := &w.images[i]
		if entry.unavailableSize != nil {
			return *entry.unavailableSize != size
		}
		return entry.columns > max(1, size.width-8) && entry.rowCount() > 0
	}
	if needs(w.selected) {
		return w.selected
	}
	for i, start := range w.galleryStarts {
		end := len(w.displayRows)
		if i+1 < len(w.galleryStarts) {
			end = w.galleryStarts[i+1]
		}
		if start < w.offset+w.viewportRows && end > w.offset && needs(i) {
			return i
		}
	}
	return -1
}
