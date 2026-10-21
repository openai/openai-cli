package custom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/imageoutput"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"github.com/openai/openai-cli/internal/terminalimage"
	"github.com/urfave/cli/v3"
)

type imagePickerWorkspaceKey struct{}
type imagePickerWorkspaceTick struct{ started time.Time }
type imagePickerWorkspaceDone struct {
	err        error
	draftSaved bool
}
type imagePickerWorkspaceRows struct{ rows []string }
type imagePickerWorkspaceReceipts struct{ rows []string }
type imagePickerWorkspaceStage imageLoadingStage

// The UI goroutine owns the screen and history. Request workers publish events;
// the receipt list also survives cancellation before the UI consumes an event.
type imagePickerWorkspace struct {
	out                                *os.File
	context                            context.Context
	root                               *cli.Command
	send                               func(tea.Msg)
	rows, painted                      []string
	displayRows                        []string
	layoutWidth, layoutCount           int
	offset, top, width, height         int
	origin                             int
	originKnown, originQueryPending    bool
	frameRevision, originQueryRevision uint64
	header                             tea.View
	started, kitty                     bool
	animationFrame                     int
	stage                              imageLoadingStage
	since                              time.Time
	receiptMu                          sync.Mutex
	receipts                           []string
	images                             []imagePickerWorkspaceImage
	selected                           int
	expanded                           bool
	mouse                              imagePickerMouseModes
	expandedRows                       []string
	expandedIndex                      int
	expandedSize                       interactiveImageSize
	previewRevision                    uint64
	previewCancel                      context.CancelFunc
	previewWorkers                     sync.WaitGroup
	pendingReceipts                    []string
	opening                            bool
	openWorkers                        sync.WaitGroup
	openError                          bool
	openErrorIndex                     int
	followSelection                    bool
	viewAfter                          time.Time
	openCancel                         context.CancelFunc
	openImage                          func(context.Context, string) error
	fontNotice                         bool // Owned by the sequential request worker.
}

func (w *imagePickerWorkspace) saved(ctx context.Context, files []imageoutput.SavedImage, mode string) error {
	var receipts []string
	for _, file := range files {
		w.receiptMu.Lock()
		w.receipts = append(w.receipts, file.Path)
		w.receiptMu.Unlock()
		receipts = append(receipts, fmt.Sprintf("Saved image: %+q", file.Path))
	}
	if len(receipts) > 0 {
		w.send(imagePickerWorkspaceReceipts{receipts})
	}
	for _, file := range files {
		protocol := savedImageProtocol(mode, true, os.Getenv)
		if protocol == "" {
			w.send(imagePickerWorkspaceImage{file: file})
			continue
		}
		img, err := terminalimage.ReadSavedMatching(ctx, file.Path, file.SHA256)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			w.send(imagePickerWorkspaceImage{file: file, protocol: protocol, rows: []string{"Preview unavailable. Open the saved file; no need to generate again."}})
			continue
		}
		width, height, err := term.GetSize(w.out.Fd())
		if err != nil {
			return err
		}
		cw, ch := terminalimage.CellSize(w.out.Fd())
		form := imagePicker{height: height, workspace: true}
		columns := imagePickerPreviewColumns(img.Bounds(), width, max(3, height-form.viewHeight()-2), cw, ch)
		var rows []string
		if columns > 0 {
			requested := protocol
			rows, protocol, err = imagePickerPreviewRows(ctx, w.out, img, protocol, columns)
			if err == nil && requested == "font" && protocol == "blocks" {
				w.fontPreviewNotice("Color preview. Saved files retain full quality.")
			}
		}
		if err != nil || columns < 1 {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var fontErr *terminalimage.FontError
			if err != nil && !errors.As(err, &fontErr) {
				return err
			}
			rows = []string{"Preview unavailable. Open the saved file; no need to generate again."}
			if fontErr != nil {
				w.fontPreviewNotice("Sharp previews unavailable. Open the saved files to view them.")
				rows = nil
			}
		}
		w.send(imagePickerWorkspaceImage{file: file, protocol: protocol, rows: rows})
	}
	return nil
}

func (w *imagePickerWorkspace) fontPreviewNotice(message string) {
	if !w.fontNotice {
		w.fontNotice = true
		w.send(imagePickerWorkspaceRows{[]string{message}})
	}
}

func (w *imagePickerWorkspace) scroll(delta int) {
	w.offset = max(0, min(w.offset+delta, max(0, len(w.displayRows)-max(0, w.height-w.top-2))))
}

func (w *imagePickerWorkspace) progress() string {
	label := "Generating image"
	if w.stage == imageLoadingSaving {
		label = "Saving image"
	}
	if w.stage == imageLoadingSaved {
		label = "Images saved"
	}
	if loadingAnimationSupported(os.Getenv) {
		animation := imageLoadingSpinner(os.Getenv, runtime.GOOS)
		label = animation.Frames[w.animationFrame%len(animation.Frames)] + " " + label
	}
	return fmt.Sprintf("%s · %ds elapsed", label, int(time.Since(w.since)/time.Second))
}

func (w *imagePickerWorkspace) draw(m *imagePicker, profile colorprofile.Profile, busy, headerChanged bool) error {
	width, height := m.width, m.height
	if width <= 0 || height <= 0 {
		return nil
	}
	resized := width != w.width || height != w.height
	if resized || headerChanged || w.header.Content == "" {
		view := m.View()
		var text strings.Builder
		_, _ = io.WriteString(&colorprofile.Writer{Forward: &text, Profile: max(profile, colorprofile.ASCII)}, view.Content)
		view.Content = text.String()
		w.header = view
	}
	w.width, w.height = width, height
	header := strings.Split(w.header.Content, "\n")
	w.top = len(header)
	if w.layoutWidth != width || w.layoutCount != len(w.rows) {
		w.displayRows = nil
		image := 0
		for i, row := range w.rows {
			if image < len(w.images) && w.images[image].start == i {
				w.images[image].displayStart = len(w.displayRows)
				image++
			}
			if strings.Contains(row, "\x1b_G") || strings.Contains(row, "\x1b]1337;") {
				w.displayRows = append(w.displayRows, row)
			} else {
				w.displayRows = append(w.displayRows, strings.Split(ansi.Wrap(row, max(1, width-8), ""), "\n")...)
			}
		}
		w.layoutWidth, w.layoutCount = width, len(w.rows)
	}
	if w.followSelection && len(w.images) > 0 {
		if height-w.top-2 >= 3 {
			w.offset = w.images[w.selected].displayStart
		}
		w.followSelection = false
	}
	w.scroll(0)
	lines := append([]string(nil), header...)
	if w.expanded && len(w.images) > 0 {
		lines = w.expandedView(m, busy)
	} else {
		if busy || len(w.rows) > 0 || m.note != "" {
			status := ""
			if len(w.rows) > 0 {
				status = "Images"
				if len(w.images) > 0 {
					status += fmt.Sprintf(" · %d of %d", w.selected+1, len(w.images))
				}
				if m.focus == "images" && !busy {
					status = "› " + status
				}
			}
			if busy {
				if status != "" {
					status += " · "
				}
				status += w.progress()
			} else if m.note != "" {
				if status != "" {
					status += " · "
				}
				status += imagePickerLine(m.note)
			}
			if len(w.displayRows) > 0 {
				if w.offset > 0 {
					status += " ↑"
				}
				if w.offset+max(0, height-w.top-2) < len(w.displayRows) {
					status += " ↓"
				}
			}
			lines = append(lines, m.workspaceImageFrame(status, width, true))
		}
		if len(w.rows) > 0 && width >= 40 && height >= 12 {
			available := min(len(w.displayRows), max(0, height-len(lines)-1))
			for i := 0; i < available; i++ {
				row := ""
				if w.offset+i < len(w.displayRows) {
					row = w.displayRows[w.offset+i]
				}
				if !strings.Contains(row, "\x1b_G") && !strings.Contains(row, "\x1b]1337;") {
					row = ansi.Truncate(row, max(0, width-8), "…")
				}
				lines = append(lines, m.workspaceImageRow(row, width))
			}
			label := ""
			if len(w.images) > 0 {
				label = "Saved: " + imagePickerLine(imagePickerFolderLabel(w.images[w.selected].file.Path))
				if w.openError && w.openErrorIndex == w.selected {
					label = "Open failed · " + label
				}
			}
			lines = append(lines, m.workspaceImageFrame(label, width, false))
		} else if busy || m.note != "" {
			lines = append(lines, m.workspaceImageFrame("", width, false))
		}
	}
	lines = lines[:min(len(lines), height)]
	var output strings.Builder
	if !w.started {
		// Start a separate logical line, away from the shell's prompt mark.
		output.WriteString("\r\n")
		w.started = true
	}
	if len(w.images) > 0 {
		output.WriteString(w.mouse.enable())
	}
	output.WriteString(ansi.ResetModeTextCursorEnable)
	receipts := len(w.pendingReceipts) > 0
	if receipts {
		// Preserve complete stream receipts even when history is scrolled away.
		// Restore the same panel within this frame before optional preview bytes.
		if len(w.painted) > 0 {
			w.clearGraphics(&output)
			output.WriteString(ansi.RestoreCursor)
		}
		output.WriteString(cliterminal.InlineASCIIRecords(w.pendingReceipts, width))
		output.WriteString(ansi.SaveCursor)
		w.pendingReceipts = nil
	}
	// The existing inline helper reserves one reflowable panel. Stable frames
	// repaint changed rows only; resize and view changes replace that panel.
	if receipts || resized || len(lines) != len(w.painted) || len(w.painted) == 0 {
		w.frameRevision++
		w.origin, w.originKnown = 0, len(lines) == height
		if len(w.painted) > 0 {
			w.clearGraphics(&output)
			output.WriteString(ansi.RestoreCursor)
		}
		output.WriteString(cliterminal.InlineFrame(strings.Join(lines, "\n"), width))
		output.WriteString(ansi.SaveCursor)
	} else {
		for i, line := range lines {
			if line == w.painted[i] {
				continue
			}
			output.WriteString(ansi.RestoreCursor)
			if i > 0 {
				output.WriteString(ansi.CursorDown(i))
			}
			if w.kitty {
				output.WriteString("\x1b[5G\x1b_Ga=d,d=C,q=2;\x1b\\")
			}
			output.WriteString("\r\x1b[0K" + line)
		}
	}
	for _, line := range lines {
		w.kitty = w.kitty || strings.Contains(line, "\x1b_G")
	}
	output.WriteString(ansi.RestoreCursor)
	if !w.originKnown && !w.originQueryPending && w.originQueryRevision != w.frameRevision {
		// Report the saved panel origin, before returning to the prompt cursor.
		w.originQueryPending, w.originQueryRevision = true, w.frameRevision
		output.WriteString(ansi.RequestCursorPositionReport)
	}
	if !busy && !w.expanded && w.header.Cursor != nil {
		cursor := w.header.Cursor
		if cursor.Y > 0 {
			output.WriteString(ansi.CursorDown(cursor.Y))
		}
		output.WriteString(ansi.CursorHorizontalAbsolute(cursor.X + 1))
		output.WriteString(ansi.SetCursorStyle(5) + ansi.SetModeTextCursorEnable)
	}
	w.painted = lines
	ctx := w.context
	if ctx == nil {
		ctx = context.Background()
	}
	writeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	protocol := ""
	if w.kitty {
		protocol = "kitty"
	}
	return terminalimage.WriteFrame(writeCtx, w.out, output.String(), protocol)
}

func (w *imagePickerWorkspace) clearGraphics(output *strings.Builder) {
	if !w.kitty {
		return
	}
	for row := range len(w.painted) {
		output.WriteString(ansi.RestoreCursor)
		if row > 0 {
			output.WriteString(ansi.CursorDown(row))
		}
		output.WriteString("\x1b[5G\x1b_Ga=d,d=C,q=2;\x1b\\")
	}
}

func (w *imagePickerWorkspace) close() error {
	mouseErr := w.stopMouse()
	if w.openCancel != nil {
		w.openCancel()
	}
	w.openWorkers.Wait()
	if w.previewCancel != nil {
		w.previewCancel()
		w.previewCancel = nil
	}
	w.previewWorkers.Wait()
	if !w.started {
		return mouseErr
	}
	w.started = false
	var output strings.Builder
	w.clearGraphics(&output)
	output.WriteString(ansi.RestoreCursor)
	output.WriteString(cliterminal.InlineFrame("", w.width))
	output.WriteString(ansi.SetCursorStyle(0) + ansi.SetModeTextCursorEnable)
	w.receiptMu.Lock()
	defer w.receiptMu.Unlock()
	for _, path := range w.receipts {
		fmt.Fprintf(&output, "Saved image: %+q\n", path)
	}
	ctx := w.context
	if ctx == nil {
		ctx = context.Background()
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 250*time.Millisecond)
	defer cancel()
	protocol := ""
	if w.kitty {
		protocol = "kitty"
	}
	return errors.Join(mouseErr, terminalimage.WriteFrame(cleanup, w.out, output.String(), protocol))
}

func imagePickerWorkspaceClock(started time.Time) tea.Cmd {
	interval := time.Second
	if loadingAnimationSupported(os.Getenv) {
		interval = imageLoadingSpinner(os.Getenv, runtime.GOOS).FPS
	}
	return tea.Tick(interval, func(time.Time) tea.Msg { return imagePickerWorkspaceTick{started} })
}
