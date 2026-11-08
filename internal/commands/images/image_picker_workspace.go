package images

import (
	"context"
	"errors"
	"fmt"
	clioutput "github.com/openai/openai-cli/internal/output"
	"image"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/imagegallery"
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
type imagePickerWorkspaceStage clioutput.LoadingStage

// The UI goroutine owns the screen and history. Request workers publish events;
// the receipt list also survives cancellation before the UI consumes an event.
type imagePickerWorkspace struct {
	out                                       *os.File
	context                                   context.Context
	root                                      *cli.Command
	send                                      func(tea.Msg)
	styleDark, styleColor                     bool
	styleProfile                              colorprofile.Profile
	rows, painted                             []string
	detailTones                               map[int]imagePickerTone
	requestFailure                            string
	displayRows                               []string
	layoutWidth, layoutCount, layoutSelected  int
	messageRows                               int
	galleryStarts                             []int
	galleryFirst, requestFirstImage           int
	galleryRevision, layoutRevision           uint64
	studio                                    *imageStudioChrome
	longNotice                                string
	longNoticeTone                            imagePickerTone
	layoutCaptions                            bool
	galleryJump, galleryDetails               bool
	viewportRows, bodyStart, bodyEnd          int
	offset, top, width, height                int
	origin                                    int
	originKnown, originQueryPending           bool
	frameRevision, originQueryRevision        uint64
	header                                    tea.View
	started, kitty                            bool
	animationFrame                            int
	stage                                     clioutput.LoadingStage
	since                                     time.Time
	receiptMu                                 sync.Mutex
	receipts                                  []string
	images                                    []imagePickerWorkspaceImage
	nameCounts                                map[string]int
	selected                                  int
	expanded                                  bool
	iterm, konsole, konsoleScreen             bool
	konsoleViewport                           interactiveImageSize
	nativeTransfer                            *imagePickerNativeTransfer
	transferWorkers                           sync.WaitGroup
	mouse                                     imagePickerMouseModes
	expandedRows                              []string
	expandedNativeRows, expandedColumns       int
	nativeCache                               map[imagePickerNativeRow]imagePickerNativePayload
	nativeBytes                               int
	nativeClock                               uint64
	nativeTransient                           map[imagePickerNativeRow]string
	nativeDisplay, nativeFrame, nativePainted map[int]imagePickerNativeRow
	nativeReady                               map[int]bool
	nativeChrome                              map[int]imagePickerStudioNativeChrome
	nativeColumn                              int // One-based column of the last successful graphics frame.
	failedColumn, failedRows                  int // A transport error can leave part of the attempted frame visible.
	expandedIndex                             int
	expandedSize                              interactiveImageSize
	previewRevision                           uint64
	previewPending                            bool
	previewIndex                              int
	previewSize                               interactiveImageSize
	previewExpanded                           bool
	previewReload                             bool
	previewColumns                            int
	previewErrorMu                            sync.Mutex
	previewError                              error
	prepareRows                               func(context.Context, *os.File, image.Image, string, int) ([]string, string, error)
	previewCancel                             context.CancelFunc
	previewWorkers                            sync.WaitGroup
	previewGate                               chan struct{}
	fontPreviewOnce                           sync.Once
	fontPreviewGate                           chan struct{}
	opening                                   bool
	openWorkers                               sync.WaitGroup
	openError                                 bool
	openErrorIndex                            int
	viewAfter                                 time.Time
	openCancel                                context.CancelFunc
	openImage                                 func(context.Context, string) error
	fontNotice                                bool // Owned by the sequential request worker.
}

func (w *imagePickerWorkspace) saved(ctx context.Context, files []imageoutput.SavedImage, mode string) error {
	for _, file := range files {
		w.receiptMu.Lock()
		w.receipts = append(w.receipts, file.Path)
		w.receiptMu.Unlock()
	}
	selectedProtocol := savedImageProtocol(mode, true, os.Getenv)
	if selectedProtocol == "font" {
		// Retain the gallery for the whole batch. Expanded and resized previews
		// wait outside the input loop instead of competing for its file lock.
		release, err := w.acquireFontPreview(ctx)
		if err != nil {
			return err
		}
		defer release()
	}
	prepare := w.prepareRows
	if prepare == nil {
		prepare = imagePickerPreviewRows
	}
	for _, file := range files {
		protocol := selectedProtocol
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
		size, err := interactiveImageViewport(w.out)
		if err != nil {
			return err
		}
		columns := imagePickerInlinePreviewColumns(img.Bounds(), size, protocol)
		var rows []string
		if columns > 0 {
			rows, protocol, err = prepare(ctx, w.out, img, protocol, columns)
		}
		if err != nil || columns < 1 {
			if ctx.Err() != nil {
				return errors.Join(err, ctx.Err())
			}
			var fontErr *terminalimage.FontError
			if err != nil && !errors.As(err, &fontErr) {
				return err
			}
			rows = []string{"Preview unavailable. Open the saved file; no need to generate again."}
			if fontErr != nil {
				if errors.Is(err, imagegallery.ErrFull) {
					w.fontPreviewNotice("Sharp-preview capacity reached. Open the saved image to view it at full quality.")
				} else {
					w.fontPreviewNotice("Sharp previews unavailable. Open the saved files to view them.")
				}
				rows = nil
			}
		}
		g, _ := imageStudioLayout(size.width, size.height)
		entry := imagePickerWorkspaceImage{file: file, protocol: protocol, rows: rows, columns: columns, preparedRows: g.previewRows}
		if columns < 1 {
			entry.unavailableSize = &size
		}
		w.send(entry)
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
	before := w.offset
	limit := max(0, len(w.displayRows)-w.viewportRows)
	if delta >= limit {
		w.offset = limit
	} else if delta <= -limit {
		w.offset = 0
	} else {
		w.offset = max(0, min(w.offset+delta, limit))
	}
	if delta != 0 && w.offset != before && len(w.galleryStarts) > 0 && w.offset >= w.galleryStarts[0] {
		w.selected = w.galleryImageAt(w.offset + w.viewportRows/2)
		if w.offset == limit {
			w.selected = w.galleryFirst + len(w.galleryStarts) - 1
		}
		if w.offset == 0 {
			w.selected = w.galleryFirst
		}
		w.images[w.selected].inlineOffset = max(0, w.offset-w.galleryStart(w.selected))
	}
}

func (w *imagePickerWorkspace) progress(m *imagePicker) string {
	label := "Generating image"
	if w.stage == clioutput.LoadingSaving {
		label = "Saving image"
	}
	if w.stage == clioutput.LoadingSaved {
		label = "Preparing previews"
	}
	if clioutput.LoadingAnimationSupported(os.Getenv) {
		animation := clioutput.LoadingSpinner(os.Getenv, runtime.GOOS)
		frame := animation.Frames[w.animationFrame%len(animation.Frames)]
		label = m.workspaceText(frame, imageActive) + " " + label
	}
	return label + m.workspaceText(fmt.Sprintf(" · %ds elapsed", int(time.Since(w.since)/time.Second)), imageSecondary)
}

func (w *imagePickerWorkspace) draw(m *imagePicker, profile colorprofile.Profile, busy, headerChanged bool) (err error) {
	if transfer := w.nativeTransfer; transfer != nil {
		size := interactiveImageSize{width: m.width, height: m.height}
		expanded := transfer.expanded
		if transfer.inlineColumn == 0 {
			size.cellWidth, size.cellHeight = terminalimage.CellSize(w.out.Fd())
			expanded = true
		}
		if w.expanded != expanded || w.selected != transfer.index || size != transfer.size {
			select {
			case transfer.replace <- struct{}{}:
			default:
			}
		}
		return nil
	}
	defer func() {
		w.nativeTransient = nil
		err = imagePickerDisplayFailure(err)
	}()
	width, height := m.width, m.height
	if width <= 0 || height <= 0 {
		return nil
	}
	ctx := w.context
	if ctx == nil {
		ctx = context.Background()
	}
	nativeExpanded := w.konsole && w.expanded && w.selected >= 0 && w.selected < len(w.images) && w.images[w.selected].protocol != ""
	if err := w.setKonsoleScreen(ctx, nativeExpanded); err != nil {
		return err
	}
	resized := width != w.width || height != w.height
	if w.konsoleScreen {
		cw, ch := terminalimage.CellSize(w.out.Fd())
		size := interactiveImageSize{width, height, cw, ch}
		resized = resized || size != w.konsoleViewport
		w.konsoleViewport = size
	}
	styleChanged := w.styleDark != m.dark || w.styleColor != m.color || w.styleProfile != profile
	if styleChanged {
		w.styleDark, w.styleColor, w.styleProfile = m.dark, m.color, profile
		w.galleryRevision++ // Recolor cached diagnostics without changing their scroll anchor.
	}
	m.profile = profile
	if resized || headerChanged || styleChanged || w.header.Content == "" {
		view := m.View()
		var text strings.Builder
		_, _ = io.WriteString(&colorprofile.Writer{Forward: &text, Profile: max(profile, colorprofile.ASCII)}, view.Content)
		view.Content = text.String()
		w.header = view
		w.studio = nil
	}
	w.width, w.height = width, height
	w.nativeFrame = make(map[int]imagePickerNativeRow)
	w.nativeReady = make(map[int]bool)
	w.nativeChrome = make(map[int]imagePickerStudioNativeChrome)
	var lines []string
	if w.expanded && len(w.images) > 0 {
		lines = w.expandedView(m, busy)
	} else {
		lines = w.inlineView(m, busy)
	}
	lines = lines[:min(len(lines), height)]
	if w.konsoleScreen {
		return w.drawKonsoleFrame(ctx, lines, resized)
	}
	column := 5
	if g, ok := imageStudioLayout(width, height); ok && !w.expanded {
		column = g.previewX + 1
	}
	repaint := resized || len(lines) != len(w.painted) || column != w.nativeColumn || len(w.painted) == 0
	keys, placements := w.nativePaintKeys(lines, repaint)
	var output strings.Builder
	if !w.started {
		// The frame separates its title from the shell's prompt mark. Only
		// unframed layouts need another logical line before their title.
		if m.width < 50 || m.viewHeight() < 16 {
			output.WriteString("\r\n")
		}
		w.started = true
	}
	if len(w.images) > 0 {
		output.WriteString(w.mouse.enable())
	}
	output.WriteString(ansi.ResetModeTextCursorEnable)
	// The existing inline helper reserves one reflowable panel. Stable frames
	// repaint changed rows only; resize and view changes replace that panel.
	if repaint {
		w.frameRevision++
		w.origin, w.originKnown = 0, len(lines) == height
		if len(w.painted) > 0 {
			w.clearGraphics(&output)
			output.WriteString(ansi.RestoreCursor)
		}
		for i, line := range lines {
			lines[i] = w.nativeWireLine(i, line)
		}
		output.WriteString(cliterminal.InlineFrame(strings.Join(lines, "\n"), width))
		output.WriteString(ansi.SaveCursor)
	} else {
		for i, line := range lines {
			if keys[i] == w.painted[i] {
				continue
			}
			output.WriteString(ansi.RestoreCursor)
			if i > 0 {
				output.WriteString(ansi.CursorDown(i))
			}
			old, placed := w.nativePainted[i]
			if chrome, shared := w.nativeChrome[i]; shared && placed && placements[i] == old {
				// Clear only the editor cells. Full-line erasure destroys iTerm placements.
				output.WriteString("\r" + ansi.EraseCharacter(column-1) + chrome.prefix + chrome.suffix)
				continue
			}
			if w.kitty && placed {
				output.WriteString(imagePickerDeleteGraphics(column))
			}
			lines[i] = w.nativeWireLine(i, line)
			output.WriteString("\r\x1b[0K" + lines[i])
		}
	}
	for _, line := range lines {
		w.kitty = w.kitty || strings.Contains(line, "\x1b_G")
	}
	output.WriteString(ansi.RestoreCursor)
	// A replaced transfer may never emit its final controls. The completion
	// redraw sends a new cursor query after the native bytes have committed.
	transferImage := w.iterm && strings.Contains(output.String(), "\x1b]1337;File=")
	if !transferImage && !w.originKnown && !w.originQueryPending && w.originQueryRevision != w.frameRevision {
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
	if transferImage {
		return w.startNativeTransfer(ctx, []string{output.String()}, keys, placements, column)
	}
	writeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	protocol := ""
	if w.kitty {
		protocol = "kitty"
	}
	if err := terminalimage.WriteFrame(writeCtx, w.out, output.String(), protocol); err != nil {
		w.nativePainted = nil
		w.failedColumn, w.failedRows = column, len(lines)
		return err
	}
	w.nativeColumn = column
	w.painted, w.nativePainted = keys, placements
	w.retainNativeFrame()
	return nil
}

func (w *imagePickerWorkspace) clearGraphics(output *strings.Builder) {
	w.nativePainted = nil
	if !w.kitty {
		return
	}
	clear := func(column, rows int) {
		for row := range rows {
			output.WriteString(ansi.RestoreCursor)
			if row > 0 {
				output.WriteString(ansi.CursorDown(row))
			}
			output.WriteString(imagePickerDeleteGraphics(column))
		}
	}
	clear(w.nativeColumn, len(w.painted))
	clear(w.failedColumn, w.failedRows)
	w.failedRows = 0
}

func imagePickerDeleteGraphics(column int) string {
	return ansi.CursorHorizontalAbsolute(column) + "\x1b_Ga=d,d=C,q=2;\x1b\\"
}

func (w *imagePickerWorkspace) close() error {
	mouseErr := w.stopMouse()
	w.transferWorkers.Wait()
	if w.openCancel != nil {
		w.openCancel()
	}
	w.openWorkers.Wait()
	if w.previewCancel != nil {
		w.previewCancel()
		w.previewCancel = nil
	}
	w.previewWorkers.Wait()
	w.previewErrorMu.Lock()
	previewErr := w.previewError
	w.previewErrorMu.Unlock()
	ctx := w.context
	if ctx == nil {
		ctx = context.Background()
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 250*time.Millisecond)
	defer cancel()
	screenErr := w.setKonsoleScreen(cleanup, false)
	if !w.started {
		return errors.Join(mouseErr, previewErr, screenErr)
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
	protocol := ""
	if w.kitty {
		protocol = "kitty"
	}
	return errors.Join(mouseErr, previewErr, imagePickerDisplayFailure(errors.Join(screenErr, terminalimage.WriteFrame(cleanup, w.out, output.String(), protocol))))
}

func imagePickerWorkspaceClock(started time.Time) tea.Cmd {
	interval := time.Second
	if clioutput.LoadingAnimationSupported(os.Getenv) {
		interval = clioutput.LoadingSpinner(os.Getenv, runtime.GOOS).FPS
	}
	return tea.Tick(interval, func(time.Time) tea.Msg { return imagePickerWorkspaceTick{started} })
}
