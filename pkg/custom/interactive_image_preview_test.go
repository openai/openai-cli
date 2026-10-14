package custom

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestInteractiveImageEligibility(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows", "freebsd"} {
		t.Run(goos, func(t *testing.T) {
			getenv := func(key string) string {
				return map[string]string{"TERM": "xterm-256color", "KONSOLE_VERSION": "220400"}[key]
			}
			require.Equal(t, goos == "darwin" || goos == "linux", interactiveImageEligible(true, true, getenv, goos))
		})
	}
	for _, tc := range []struct{ key, value string }{
		{"TERM", "dumb"}, {"TERM", "screen-256color"}, {"TERM", "tmux-256color"},
		{"TERM", "xterm-kitty"}, {"TERM", "xterm-ghostty"}, {"TERM_PROGRAM", "vscode"},
		{"TERM_PROGRAM", "iTerm.app"}, {"TERM_PROGRAM", "unknown"}, {"CI", "true"},
		{"CI", "1"}, {"TMUX", "socket"}, {"STY", "screen"}, {"ZELLIJ", "session"},
		{"KONSOLE_VERSION", ""}, {"KONSOLE_VERSION", "220399"}, {"KONSOLE_VERSION", "99999"},
		{"KONSOLE_VERSION", "+99999"}, {"KONSOLE_VERSION", "9999999"}, {"KONSOLE_VERSION", "abcdef"},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			env := map[string]string{"TERM": "xterm-256color", "KONSOLE_VERSION": "261170", tc.key: tc.value}
			require.False(t, interactiveImageEligible(true, true, func(key string) string { return env[key] }, "linux"))
		})
	}
	for _, ci := range []string{"", "0", "false", "FALSE"} {
		for _, optIn := range []string{"", "0", "1"} {
			getenv := func(key string) string {
				return map[string]string{"KONSOLE_VERSION": "261170", "CI": ci, "OPENAI_KONSOLE_IMAGES": optIn}[key]
			}
			require.True(t, interactiveImageEligible(true, true, getenv, "linux"))
			require.False(t, interactiveImageEligible(false, true, getenv, "linux"))
			require.False(t, interactiveImageEligible(true, false, getenv, "linux"))
		}
	}
}

func TestInteractiveImageKeys(t *testing.T) {
	for _, tc := range []struct {
		name, data    string
		expired, stop bool
		code          int
	}{
		{"quit", "q", false, true, 0}, {"interrupt", "\x03", false, true, 130},
		{"escape delayed", "\x1b", false, false, 0}, {"escape", "\x1b", true, true, 0},
		{"up arrow", "\x1b[A", true, false, 0}, {"down arrow", "\x1b[B", true, false, 0},
		{"alt q", "\x1bq", true, false, 0}, {"ordinary", "hello", true, false, 0},
		{"paste", "\x1b[200~q\x03\x1b\x1b[201~", true, false, 0},
		{"quit after paste", "\x1b[200~q\x03\x1b[201~q", false, true, 0},
		{"malformed", "\x1b[123456", true, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parser := interactiveImageKeys{}
			stop, code := false, 0
			for _, value := range []byte(tc.data) {
				stop, code = parser.feed(value)
				if stop {
					break
				}
			}
			if !stop && tc.expired {
				stop, code = parser.decode(true)
			}
			require.Equal(t, tc.stop, stop)
			require.Equal(t, tc.code, code)
		})
	}
}

func TestInteractiveImageKeysBoundInput(t *testing.T) {
	parser := interactiveImageKeys{}
	for _, value := range []byte("\x1b[200~") {
		parser.feed(value)
	}
	for i := 0; i < 2*1024*1024; i++ {
		stop, _ := parser.feed('q')
		require.False(t, stop)
		require.LessOrEqual(t, len(parser.pending), 64)
	}
	for _, value := range []byte("\x1b[201~") {
		parser.feed(value)
	}
	stop, code := parser.feed('q')
	require.True(t, stop)
	require.Zero(t, code)
	parser = interactiveImageKeys{}
	for _, value := range []byte("\x1b[" + strings.Repeat("1", 128*1024)) {
		parser.feed(value)
		require.LessOrEqual(t, len(parser.pending), 64)
	}
}

type interactiveImageTestInput struct {
	*io.PipeReader
	once sync.Once
}

func (r *interactiveImageTestInput) Cancel() bool {
	r.once.Do(func() { _ = r.Close() })
	return true
}

func TestInteractiveImageInputJoinsBlockedReader(t *testing.T) {
	for _, data := range []string{"q", "\x1b", "\x03", ""} {
		t.Run(base64.StdEncoding.EncodeToString([]byte(data)), func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			reader, writer := io.Pipe()
			defer writer.Close()
			source := &interactiveImageTestInput{PipeReader: reader}
			stop := context.AfterFunc(ctx, func() { source.Cancel() })
			defer stop()
			done := make(chan struct{})
			go func() { readInteractiveImageKeys(ctx, source, cancel); close(done) }()
			if data != "" {
				_, err := io.WriteString(writer, data)
				require.NoError(t, err)
			} else {
				cancel(context.Canceled)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("input reader did not stop")
			}
			if data != "" {
				var exited interactiveImageExit
				require.ErrorAs(t, context.Cause(ctx), &exited)
				expected := 0
				if data == "\x03" {
					expected = 130
				}
				require.Equal(t, expected, exited.code)
			}
		})
	}
}

func TestInteractiveImageFramesKeepPixelsAndFit(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 16))
	img.Set(0, 0, color.NRGBA{255, 0, 0, 255})
	img.Set(31, 15, color.NRGBA{0, 255, 0, 255})
	for _, size := range []interactiveImageSize{{80, 24, 8, 16}, {20, 10, 8, 16}} {
		var out bytes.Buffer
		require.NoError(t, paintInteractiveImage(t.Context(), &out, img, size))
		text := out.String()
		require.True(t, strings.HasPrefix(text, "\x1b[2J\x1b[Hq / Esc: close\x1b[2;1H\x1b]1337;"))
		start := strings.Index(text, "\x1b]1337;")
		payload := text[start:]
		_, encoded, ok := strings.Cut(payload, ":")
		require.True(t, ok)
		pngData, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(encoded, "\x07"))
		require.NoError(t, err)
		decoded, err := png.Decode(bytes.NewReader(pngData))
		require.NoError(t, err)
		require.Equal(t, img.Bounds(), decoded.Bounds())
		for y := 0; y < 16; y++ {
			for x := 0; x < 32; x++ {
				require.Equal(t, color.NRGBAModel.Convert(img.At(x, y)), color.NRGBAModel.Convert(decoded.At(x, y)))
			}
		}
		if size.width == 20 {
			require.Contains(t, text, "width=19;")
		}
	}
	var small bytes.Buffer
	require.NoError(t, paintInteractiveImage(t.Context(), &small, img, interactiveImageSize{2, 2, 8, 16}))
	require.NotContains(t, small.String(), "1337")
	require.Equal(t, "\x1b[2J\x1b[HE", small.String())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var cancelled bytes.Buffer
	require.ErrorIs(t, paintInteractiveImage(ctx, &cancelled, img, interactiveImageSize{80, 24, 8, 16}), context.Canceled)
	require.Zero(t, cancelled.Len())
	writer := previewNoticeWriter(func([]byte) (int, error) { return 0, io.ErrClosedPipe })
	require.True(t, errors.Is(paintInteractiveImage(t.Context(), writer, img, interactiveImageSize{80, 24, 8, 16}), io.ErrClosedPipe))
}

func TestInteractiveImageCancellationPreservesOtherFailures(t *testing.T) {
	writeErr := errors.New("independent output failure")
	cleanupErr := errors.New("independent reset failure")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(interactiveImageExit{0})
	for _, input := range []error{
		errors.Join(context.Canceled, writeErr, cleanupErr),
		fmt.Errorf("terminal: %w", errors.Join(context.Canceled, writeErr, cleanupErr)),
	} {
		err := interactiveImageWriteError(ctx, input)
		require.ErrorIs(t, err, writeErr)
		require.ErrorIs(t, err, cleanupErr)
		require.NotErrorIs(t, err, context.Canceled)
	}
	require.NoError(t, interactiveImageWriteError(ctx, fmt.Errorf("stopped: %w", context.Canceled)))
	input, failed := context.WithCancelCause(t.Context())
	failed(io.ErrUnexpectedEOF)
	err := interactiveImageWriteError(input, context.Canceled)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.NotErrorIs(t, err, context.Canceled)
}

func TestInteractiveImageFailureKeepsExitCodeAndViewerDiagnostic(t *testing.T) {
	for _, code := range []int{0, 130, 143, 129} {
		ctx, cancel := context.WithCancelCause(t.Context())
		cancel(interactiveImageExit{code})
		err := finishInteractiveImagePreview(ctx, nil)
		if code == 0 {
			require.NoError(t, err)
		} else {
			var exit cli.ExitCoder
			require.ErrorAs(t, err, &exit)
			require.Equal(t, code, exit.ExitCode())
			require.Empty(t, exit.Error())
		}
		failure := errors.Join(context.DeadlineExceeded, io.ErrClosedPipe)
		err = finishInteractiveImagePreview(ctx, failure)
		var exit cli.ExitCoder
		require.ErrorAs(t, err, &exit)
		require.Equal(t, max(1, code), exit.ExitCode())
		require.ErrorIs(t, err, failure)
		var output bytes.Buffer
		require.NoError(t, ShowCommandError(&cli.Command{}, err, &output))
		require.Contains(t, output.String(), "interactive preview")
		require.Contains(t, output.String(), "original file is unchanged")
		require.NotContains(t, output.String(), "API")
		require.NotContains(t, output.String(), "permissions")
	}
}
