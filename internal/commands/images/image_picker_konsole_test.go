package images

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/terminalimage"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestImagePickerKonsolePreviewPreservesOneCompleteImage(t *testing.T) {
	for _, bounds := range []image.Rectangle{
		image.Rect(0, 0, 64, 64), image.Rect(0, 0, 160, 960),
		image.Rect(0, 0, 640, 320), image.Rect(7, 11, 14, 20),
	} {
		t.Run(fmt.Sprint(bounds), func(t *testing.T) {
			img := image.NewNRGBA(bounds)
			for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
				for x := bounds.Min.X; x < bounds.Max.X; x++ {
					img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 17), G: uint8(y * 31), B: uint8(x + y), A: uint8(64 + (x+y)%192)})
				}
			}
			rows, protocol, err := imagePickerKonsolePreview(t.Context(), nil, img, "iterm-konsole", 64)
			require.NoError(t, err)
			require.Equal(t, "iterm-konsole", protocol)
			require.Len(t, rows, 1, "one image must use one scale, without independently fitted strips")
			header, payload, ok := strings.Cut(rows[0], ":")
			require.True(t, ok)
			require.Contains(t, header, "width=64;")
			require.Contains(t, header, "height=auto")
			require.Equal(t, 1, strings.Count(rows[0], "\x1b]1337;File="))
			encoded, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(payload, "\a"))
			require.NoError(t, err)
			decoded, err := png.Decode(bytes.NewReader(encoded))
			require.NoError(t, err)
			require.Equal(t, bounds.Size(), decoded.Bounds().Size())
			normalized := image.NewNRGBA(image.Rectangle{Max: bounds.Size()})
			draw.Draw(normalized, normalized.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
			require.Equal(t, img.Pix, normalized.Pix, "preparation must retain every source pixel and alpha value")
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	rows, _, err := imagePickerKonsolePreview(ctx, nil, image.NewNRGBA(image.Rect(0, 0, 64, 64)), "iterm-konsole", 64)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, rows)
}

func TestImagePickerKonsolePaintsImageAfterText(t *testing.T) {
	m, w := konsoleWorkspaceForTest(t)
	w.expanded = true
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	written := len(konsoleOutputForTest(t, w))
	stageKonsoleImageForTest(t, m, w)
	frame := konsoleOutputForTest(t, w)[written:]
	require.Equal(t, 1, strings.Count(frame, "\x1b]1337;File="))
	require.Less(t, strings.LastIndex(frame, "\x1b[2K"), strings.Index(frame, "\x1b]1337;File="), "later text clears must not erase image pixels")
	require.Len(t, w.nativePainted, 1)
	require.LessOrEqual(t, w.nativeBytes, imagePickerNativeCacheBytes)
}

func TestImagePickerKonsoleReportingPreservesFailures(t *testing.T) {
	require.NoError(t, imagePickerReportFailure(nil))
	canceled := cli.Exit("", 143)
	require.Same(t, canceled, imagePickerReportFailure(canceled))
	cause := errors.New("synthetic terminal restoration failure")
	failure := imagePickerReportFailure(errors.Join(canceled, imagePickerDisplayFailure(cause)))
	require.ErrorIs(t, failure, cause)
	var exit cli.ExitCoder
	require.ErrorAs(t, failure, &exit)
	require.Equal(t, 1, exit.ExitCode())
	require.NotEmpty(t, exit.Error())
	require.Contains(t, failure.Error(), "Saved images remain available.")
	ordinary := imagePickerDisplayFailure(cause)
	require.Same(t, ordinary, imagePickerReportFailure(ordinary))
}

func konsoleWorkspaceForTest(t *testing.T) (*imagePicker, *imagePickerWorkspace) {
	t.Helper()
	m, w := browserForTest(t, 96, 28)
	w.konsole = true
	entry := recoverySavedImage(t)
	entry.protocol, entry.rows = "blocks", []string{"automatic preview"}
	w.appendImage(entry)
	return m, w
}

func konsoleOutputForTest(t *testing.T, w *imagePickerWorkspace) string {
	t.Helper()
	output, err := os.ReadFile(w.out.Name())
	require.NoError(t, err)
	return string(output)
}

func stageKonsoleImageForTest(t *testing.T, m *imagePicker, w *imagePickerWorkspace) {
	t.Helper()
	rows, _, err := imagePickerKonsolePreview(t.Context(), w.out, image.NewNRGBA(image.Rect(0, 0, 32, 32)), "iterm-konsole", 8)
	require.NoError(t, err)
	w.expandedIndex, w.expandedSize = w.selected, w.konsoleViewport
	w.expandedRows = nil
	w.expandedNativeRows, w.expandedColumns = len(rows), 8
	w.stageNativeRows(w.selected, true, 8, rows)
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
}

func TestImagePickerKonsoleReturnsBeforeReceipts(t *testing.T) {
	m, w := konsoleWorkspaceForTest(t)
	m.settings.prompt = "Keep this draft"
	m.cursor, m.selected, m.focus = 4, 2, "options"
	w.receipts = []string{w.images[0].file.Path}
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	settings := m.settings
	w.expanded = true
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	stageKonsoleImageForTest(t, m, w)
	require.Contains(t, konsoleOutputForTest(t, w), "\x1b]1337;File=")
	p := &imagePickerInline{model: m, workspace: w}
	p.closeExpanded()
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	require.Equal(t, settings, m.settings)
	require.Equal(t, 4, m.cursor)
	require.Equal(t, 2, m.selected)
	require.Equal(t, "options", m.focus)
	require.Equal(t, "blocks", w.images[0].protocol)
	require.NotContains(t, konsoleOutputForTest(t, w), "Saved image:")
	w.expanded = true
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	require.NoError(t, w.close())
	require.NoError(t, w.close())
	output := konsoleOutputForTest(t, w)
	require.Equal(t, 2, strings.Count(output, "\x1b[?1049h"))
	require.Equal(t, 2, strings.Count(output, "\x1b[?1049l"))
	require.Equal(t, 1, strings.Count(output, "Saved image:"))
	require.Less(t, strings.LastIndex(output, "\x1b[?1049l"), strings.Index(output, "Saved image:"))
	bytes, err := os.ReadFile(w.images[0].file.Path)
	require.NoError(t, err)
	require.Equal(t, w.images[0].file.SHA256, sha256.Sum256(bytes))
}

func TestImagePickerKonsoleStatusKeepsUncachedPlacements(t *testing.T) {
	m, w := konsoleWorkspaceForTest(t)
	w.expanded = true
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	stageKonsoleImageForTest(t, m, w)
	for row := range w.nativeCache {
		w.removeNativeRow(row)
	}
	written := len(konsoleOutputForTest(t, w))
	m.note = "Another image finished"
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	update := konsoleOutputForTest(t, w)[written:]
	require.Contains(t, update, m.note)
	require.NotContains(t, update, "\x1b[2J")
	require.NotContains(t, update, "\x1b]1337;File=")
	_, missing := w.missingNativeRow()
	require.False(t, missing, "unchanged native placements need no payload reload")
	written = len(konsoleOutputForTest(t, w))
	m.width = 60
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	update = konsoleOutputForTest(t, w)[written:]
	require.Contains(t, update, "\x1b[2J")
	require.Contains(t, update, "Preparing preview")
	require.NotContains(t, update, "\x1b]1337;File=")
	require.Empty(t, w.nativePainted, "resizing invalidates old placements")
}

func TestImagePickerKonsolePreservesDisabledAndAutomaticProtocols(t *testing.T) {
	m, w := konsoleWorkspaceForTest(t)
	w.images[0].protocol, w.images[0].rows = "", nil
	w.expanded = true
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	output := konsoleOutputForTest(t, w)
	require.Contains(t, output, "Preview disabled or unavailable")
	require.NotContains(t, output, "\x1b]1337;File=")
	require.NotContains(t, output, "\x1b[?1049h")
	w.expanded, w.images[0].protocol = false, "blocks"
	p := &imagePickerInline{model: m, workspace: w}
	handled, command := p.updateWorkspace(imagePickerExpandedRows{
		index: 0, protocol: "iterm-konsole", columns: 8,
		rows: []string{"\x1b]1337;File=inline=1;width=8;height=1:AA==\a"},
	})
	require.True(t, handled)
	require.Nil(t, command)
	require.Equal(t, "blocks", w.images[0].protocol)
}

func TestImagePickerKonsoleCleanupRetainsWriteFailures(t *testing.T) {
	_, w := konsoleWorkspaceForTest(t)
	require.NoError(t, w.out.Close())
	require.Error(t, w.setKonsoleScreen(t.Context(), true))
	require.True(t, w.konsoleScreen, "partial screen entry requires an exit attempt")
	require.Error(t, w.close())
	require.True(t, w.konsoleScreen, "failed restoration must not claim success")
	p := &imagePickerInline{workspace: w, modesActive: true}
	p.close()
	require.Error(t, p.err)
	require.ErrorIs(t, p.err, os.ErrClosed)
}

func TestImagePickerKonsoleTinyFramesStayInsideViewport(t *testing.T) {
	for _, size := range [][2]int{{1, 1}, {2, 2}, {20, 4}, {39, 11}} {
		m, w := konsoleWorkspaceForTest(t)
		m.width, m.height = size[0], size[1]
		w.expanded = true
		require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
		output := konsoleOutputForTest(t, w)
		for _, row := range strings.Split(output, "\x1b[2K")[1:] {
			if end := strings.Index(row, "\x1b["); end >= 0 {
				row = row[:end]
			}
			require.LessOrEqual(t, ansi.StringWidth(row), max(0, m.width-1), "size %v", size)
		}
		require.NoError(t, w.close())
	}
}

func TestImagePickerKonsolePreparationReplacesQueuedWork(t *testing.T) {
	if !term.IsTerminal(os.Stdout.Fd()) {
		t.Skip("requires a private PTY")
	}
	width, height, err := term.GetSize(os.Stdout.Fd())
	require.NoError(t, err)
	require.GreaterOrEqual(t, width, 40)
	require.GreaterOrEqual(t, height, 20)
	m, w := konsoleWorkspaceForTest(t)
	m.width, m.height = width, height
	w.out, w.context, w.expanded = os.Stdout, t.Context(), true
	w.appendImage(recoverySavedImage(t))
	messages := make(chan tea.Msg, 8)
	w.send = func(msg tea.Msg) { messages <- msg }
	firstStarted, releaseFirst := make(chan struct{}), make(chan struct{})
	var active, maximum, calls atomic.Int32
	w.prepareRows = func(ctx context.Context, _ *os.File, _ image.Image, protocol string, _ int) ([]string, string, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for previous := maximum.Load(); current > previous && !maximum.CompareAndSwap(previous, current); previous = maximum.Load() {
		}
		if protocol != "iterm-konsole" {
			return nil, protocol, fmt.Errorf("unexpected expanded protocol %q", protocol)
		}
		if calls.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
			return nil, protocol, ctx.Err()
		}
		return []string{"latest prepared frame"}, protocol, nil
	}
	p := &imagePickerInline{model: m, workspace: w}
	p.prepareExpanded()
	select {
	case <-firstStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("first preparation did not start")
	}
	w.selected = 1
	p.prepareExpanded()
	w.selected = 0
	p.prepareExpanded()
	close(releaseFirst)
	select {
	case message := <-messages:
		prepared, ok := message.(imagePickerExpandedRows)
		require.True(t, ok)
		require.Equal(t, w.previewRevision, prepared.revision)
		require.Equal(t, 0, prepared.index)
		require.Equal(t, []string{"latest prepared frame"}, prepared.rows)
	case <-time.After(3 * time.Second):
		t.Fatal("latest preparation did not finish")
	}
	w.previewWorkers.Wait()
	require.EqualValues(t, 2, calls.Load(), "the replaced queued request must not prepare")
	require.EqualValues(t, 1, maximum.Load(), "only one expensive preparation may run")
	require.EqualValues(t, 0, active.Load())
	require.Empty(t, messages)
	require.Equal(t, "blocks", w.images[0].protocol)
	w.previewCancel()
}
