package images

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"image"
	"image/png"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/imageoutput"
	"github.com/openai/openai-cli/internal/terminalimage"
	"github.com/stretchr/testify/require"
)

func TestImagePickerPreviewRowsRecoversFontFailureWithoutChangingFonts(t *testing.T) {
	t.Setenv("TERM_SESSION_ID", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR", "1")
	out, err := os.CreateTemp(t.TempDir(), "screen")
	require.NoError(t, err)
	t.Cleanup(func() { out.Close() })
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	rows, protocol, err := imagePickerPreviewRows(t.Context(), out, img, "font", 8)
	require.NoError(t, err)
	require.Equal(t, "blocks", protocol)
	require.Contains(t, strings.Join(rows, ""), "▀")
	info, err := out.Stat()
	require.NoError(t, err)
	require.Zero(t, info.Size(), "preparation must not write into the owned panel")
	for _, setting := range []string{"NO_COLOR", "CLICOLOR"} {
		t.Run(setting, func(t *testing.T) {
			value := "1"
			if setting == "CLICOLOR" {
				value = "0"
			}
			t.Setenv(setting, value)
			rows, protocol, err := imagePickerPreviewRows(t.Context(), out, img, "font", 8)
			var fontErr *terminalimage.FontError
			require.ErrorAs(t, err, &fontErr)
			require.Equal(t, "font", protocol)
			require.Empty(t, rows)
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	rows, protocol, err = imagePickerPreviewRows(ctx, out, img, "font", 8)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, "font", protocol)
	require.Empty(t, rows)
	_, protocol, err = imagePickerPreviewRows(t.Context(), out, img, "invalid", 8)
	require.ErrorContains(t, err, "unsupported")
	require.Equal(t, "invalid", protocol)
	_, protocol, err = imagePickerPreviewRows(t.Context(), out, img, "font", 0)
	require.ErrorContains(t, err, "invalid image viewport")
	require.Equal(t, "font", protocol)
}

func TestImagePickerWorkspaceFrameSeparatesHeadingWithoutBlankLine(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "screen")
	require.NoError(t, err)
	t.Cleanup(func() { out.Close() })
	m := pickerForTest(t)
	m.width, m.height, m.workspace = 96, 40, true
	w := &imagePickerWorkspace{out: out}
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	require.Contains(t, ansi.Strip(w.painted[0]), "╭")
	require.Contains(t, ansi.Strip(w.painted[1]), "Create image")
	m.width = 80
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	_, err = out.Seek(0, io.SeekStart)
	require.NoError(t, err)
	raw, err := io.ReadAll(out)
	require.NoError(t, err)
	require.False(t, strings.HasPrefix(string(raw), "\r\n"))
	require.NotContains(t, string(raw), "\n", "the frame and redraws must not add separator lines")
}

func TestImagePickerWorkspaceWrappedTextRetainsEveryBoundaryByte(t *testing.T) {
	for _, width := range []int{40, 80, 160} {
		out, err := os.CreateTemp(t.TempDir(), "screen")
		require.NoError(t, err)
		t.Cleanup(func() { out.Close() })
		m := pickerForTest(t)
		m.width, m.height, m.workspace = width, 40, true
		original := strings.Repeat("0123456789", 28)
		w := &imagePickerWorkspace{out: out, rows: []string{original}}
		require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
		var reconstructed strings.Builder
		for _, row := range w.painted[w.bodyStart:w.bodyEnd] {
			reconstructed.WriteString(strings.TrimPrefix(ansi.Strip(row), "    "))
		}
		require.Equal(t, original, reconstructed.String())
	}
}

func TestImagePickerWorkspaceImageWidthReservesInset(t *testing.T) {
	for _, width := range []int{40, 49, 80, 240} {
		columns := imagePickerPreviewColumns(image.Rect(0, 0, 4096, 64), width, 40, 8, 16)
		require.LessOrEqual(t, columns, width-8)
		expanded := interactiveImageColumns(image.NewRGBA(image.Rect(0, 0, 4096, 64)), interactiveImageSize{width: width - 5, height: 40, cellWidth: 8, cellHeight: 16})
		require.LessOrEqual(t, expanded, width-6)
		require.LessOrEqual(t, columns, 64)
		require.Positive(t, columns)
	}
}

func TestImagePickerWorkspaceSeparatesImageContent(t *testing.T) {
	m := pickerForTest(t)
	for _, width := range []int{40, 96, 240} {
		require.LessOrEqual(t, ansi.StringWidth(m.workspaceImageFrame("Images", width, true)), width-2)
		require.LessOrEqual(t, ansi.StringWidth(m.workspaceImageFrame("[F2] Expand images", width, false)), width-2)
		line := ansi.Strip(m.workspaceImageRow("synthetic", width))
		require.Equal(t, "  │ synthetic│", line)
	}
	w := &imagePickerWorkspace{}
	w.appendImage(imagePickerWorkspaceImage{rows: []string{"pixels"}})
	require.Empty(t, w.rows, "captions must not enter request diagnostics")
	require.Equal(t, []string{"pixels"}, w.images[0].rows)
}

func TestImagePickerWorkspaceTinyResultRemainsVisibleAfterBack(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "screen")
	require.NoError(t, err)
	t.Cleanup(func() { out.Close() })
	m := pickerForTest(t)
	m.width, m.height, m.workspace = 40, 12, true
	w := &imagePickerWorkspace{out: out}
	w.appendImage(imagePickerWorkspaceImage{rows: []string{"pixels"}})
	w.offset = int(^uint(0) >> 1)
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	require.Contains(t, strings.Join(w.painted, "\n"), "pixels")
	before := append([]string(nil), w.painted...)
	w.expanded = true
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
	w.expanded = false
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	require.Equal(t, before, w.painted)
}

func TestImagePickerAppleTerminalSkipsModeQueries(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	t.Setenv("KITTY_WINDOW_ID", "")
	out, err := os.CreateTemp(t.TempDir(), "screen")
	require.NoError(t, err)
	defer out.Close()
	m := pickerForTest(t)
	w := &imagePickerWorkspace{}
	p := &imagePickerInline{model: m, workspace: w, output: &imagePickerOutput{File: out, cancel: func() {}}}
	p.Init()
	_, err = out.Seek(0, io.SeekStart)
	require.NoError(t, err)
	raw, err := io.ReadAll(out)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "$p")
	for _, mode := range []ansi.DECMode{1000, 1002, 1003, 1006} {
		w.mouse.report(tea.ModeReportMsg{Mode: mode, Value: ansi.ModeReset})
	}
	require.Empty(t, w.mouse.enable())
	require.Empty(t, w.mouse.disable())
}

func TestImagePickerFileURLs(t *testing.T) {
	for _, test := range []struct {
		path           string
		windows        bool
		host, pathWant string
	}{
		{"/tmp/a #?%.png", false, "", "/tmp/a #?%.png"},
		{`C:\Pictures\a #?.png`, true, "", "/C:/Pictures/a #?.png"},
		{`\\server\share\a.png`, true, "server", "/share/a.png"},
	} {
		address, err := url.Parse(imagePickerFileURL(test.path, test.windows))
		require.NoError(t, err)
		require.Equal(t, "file", address.Scheme)
		require.Equal(t, test.host, address.Host)
		require.Equal(t, test.pathWant, address.Path)
		require.Empty(t, address.RawQuery)
		require.Empty(t, address.Fragment)
	}
}

func TestImagePickerOpeningJoinsAndRejectsDuplicates(t *testing.T) {
	var data bytes.Buffer
	require.NoError(t, png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	path := filepath.Join(t.TempDir(), "a # image.png")
	require.NoError(t, os.WriteFile(path, data.Bytes(), 0600))
	started, canceled, release := make(chan string, 2), make(chan struct{}), make(chan struct{})
	messages := make(chan tea.Msg, 1)
	w := &imagePickerWorkspace{send: func(msg tea.Msg) { messages <- msg }, images: []imagePickerWorkspaceImage{{file: imageoutput.SavedImage{Path: path, SHA256: sha256.Sum256(data.Bytes())}}}}
	w.openImage = func(ctx context.Context, address string) error {
		started <- address
		<-ctx.Done()
		close(canceled)
		<-release
		return ctx.Err()
	}
	p := &imagePickerInline{workspace: w}
	require.Nil(t, p.openSavedImage())
	select {
	case address := <-started:
		require.Contains(t, address, "%23")
	case <-time.After(time.Second):
		t.Fatal("opener did not start")
	}
	require.Nil(t, p.openSavedImage())
	select {
	case <-started:
		t.Fatal("duplicate opener")
	default:
	}
	closed := make(chan struct{})
	go func() { _ = w.close(); close(closed) }()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("opener not canceled")
	}
	select {
	case <-closed:
		t.Fatal("close did not join opener")
	default:
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("close did not finish")
	}
}

func TestImagePickerOpenerDoesNotHideRequestFailure(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "screen")
	require.NoError(t, err)
	defer out.Close()
	m := pickerForTest(t)
	m.workspace = true
	m.note = "Request failed."
	p := &imagePickerInline{model: m, workspace: &imagePickerWorkspace{out: out}}
	p.updateWorkspace(imagePickerOpened{})
	require.Equal(t, "Request failed.", m.note)
}

func TestImagePickerBusyImageFocusKeepsGeometry(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "screen")
	require.NoError(t, err)
	defer out.Close()
	m := pickerForTest(t)
	m.width, m.height, m.workspace, m.generating, m.hasImages = 80, 24, true, true, true
	w := &imagePickerWorkspace{out: out, images: []imagePickerWorkspaceImage{{}}}
	p := &imagePickerInline{model: m, workspace: w, busy: true}
	require.NoError(t, w.draw(m, 0, true, true))
	boundary := w.top
	p.updateWorkspace(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, "images", m.focus)
	require.Equal(t, boundary, w.top)
	p.updateWorkspace(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, "prompt", m.focus)
	require.Equal(t, boundary, w.top)
}

func TestImagePickerChangedSavedFileDoesNotOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "changed.png")
	require.NoError(t, os.WriteFile(path, []byte("replaced"), 0600))
	messages := make(chan tea.Msg, 1)
	called := false
	w := &imagePickerWorkspace{send: func(msg tea.Msg) { messages <- msg }, images: []imagePickerWorkspaceImage{{file: imageoutput.SavedImage{Path: path, SHA256: sha256.Sum256([]byte("original"))}}}, openImage: func(context.Context, string) error { called = true; return nil }}
	p := &imagePickerInline{workspace: w}
	p.openSavedImage()
	select {
	case msg := <-messages:
		require.Error(t, msg.(imagePickerOpened).err)
	case <-time.After(time.Second):
		t.Fatal("missing failure")
	}
	require.False(t, called)
}
func TestImagePickerImageScrollAndFailureStayOnSelectedFile(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "screen")
	require.NoError(t, err)
	defer out.Close()
	m := pickerForTest(t)
	m.width, m.height, m.workspace, m.hasImages, m.focus = 96, 28, true, true, "images"
	w := &imagePickerWorkspace{out: out}
	w.appendImage(imagePickerWorkspaceImage{file: imageoutput.SavedImage{Path: "/tmp/first.png"}, rows: strings.Split(strings.Repeat("pixel\n", 30), "\n")})
	w.selected = 0
	w.offset = int(^uint(0) >> 1)
	p := &imagePickerInline{model: m, workspace: w}
	require.NoError(t, w.draw(m, 0, false, true))
	require.Equal(t, 0, w.offset)
	p.updateWorkspace(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Equal(t, 1, w.offset)
	p.updateWorkspace(tea.KeyPressMsg{Code: tea.KeyUp})
	require.Equal(t, 0, w.offset)
	p.updateWorkspace(tea.KeyPressMsg{Code: tea.KeyF2})
	require.False(t, w.expanded)
	w.appendImage(imagePickerWorkspaceImage{file: imageoutput.SavedImage{Path: "/tmp/second.png"}})
	w.selectImage(1)
	p.updateWorkspace(imagePickerOpened{err: errors.New("open failed"), index: 0})
	require.NotContains(t, ansi.Strip(strings.Join(w.painted, "\n")), "Could not open this image.")
	require.Contains(t, ansi.Strip(strings.Join(w.painted, "\n")), "second.png")
	w.selectImage(-1)
	require.NoError(t, w.draw(m, 0, false, true))
	require.Contains(t, ansi.Strip(strings.Join(w.painted, "\n")), "Could not open this image.")
}
