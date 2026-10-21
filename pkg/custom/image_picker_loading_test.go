package custom

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"charm.land/bubbles/v2/spinner"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestImagePickerLoadingShowsCompactSummaryAndBusyControls(t *testing.T) {
	m := pickerForTest(t)
	m.settings.prompt = "Synthetic horse"
	m.note = "Checking save folder… Esc cancels"
	m.result.Args = m.settings.args()
	args := append([]string(nil), m.result.Args...)
	p := newImagePickerLoading(m, io.Discard, colorprofile.ASCII)
	for _, size := range [][2]int{{80, 24}, {40, 12}} {
		for _, stage := range []imageLoadingStage{imageLoadingWaiting, imageLoadingSaving, imageLoadingSaved} {
			view := ansi.Strip(p.view(stage, 65*time.Second, size[0], size[1], ""))
			require.Contains(t, view, "Create image")
			require.Contains(t, view, "Synthetic horse")
			require.Contains(t, view, m.settings.model)
			require.Contains(t, view, "1 image")
			require.Contains(t, view, "65s elapsed")
			require.NotContains(t, view, "Settings")
			require.NotContains(t, view, "Command preview")
			require.Contains(t, view, "[Ctrl+C] Cancel")
			require.NotContains(t, view, "[Enter]")
			require.NotContains(t, view, "Checking save folder")
			require.NotContains(t, view, "Esc cancels")
			require.NotContains(t, view, "▏")
			lines := strings.Split(view, "\n")
			require.LessOrEqual(t, len(lines), p.model.viewHeight())
			for _, line := range lines {
				require.LessOrEqual(t, ansi.StringWidth(line), size[0]-2)
			}
		}
	}
	require.Equal(t, args, m.result.Args, "the busy snapshot must not change the submitted request")
}

func TestImagePickerLoadingBoundsLongPromptAndKeepsCancellationWhenTiny(t *testing.T) {
	m := pickerForTest(t)
	m.settings.prompt = strings.Repeat("👩‍💻 synthetic horse é ", 5000) + "\x1b]52;c;fake\a"
	args := m.settings.args()
	p := newImagePickerLoading(m, io.Discard, colorprofile.ASCII)
	for _, size := range [][2]int{{80, 24}, {40, 12}, {20, 4}, {80, 24}} {
		view := p.view(imageLoadingSaving, 3*time.Second, size[0], size[1], "")
		require.Contains(t, view, "Saving image")
		require.Contains(t, view, "Ctrl+C")
		require.NotContains(t, view, "\x1b]52")
		lines := strings.Split(view, "\n")
		require.LessOrEqual(t, len(lines), p.model.viewHeight())
		for _, line := range lines {
			require.LessOrEqual(t, ansi.StringWidth(line), size[0]-2)
		}
	}
	require.Equal(t, args, p.model.settings.args(), "display clipping must not change request arguments")
}

func TestImagePickerLoadingPendingSignalCannotStartRequest(t *testing.T) {
	for _, test := range []struct {
		signal os.Signal
		code   int
	}{{os.Interrupt, 130}, {syscall.SIGTERM, 143}, {syscall.SIGHUP, 129}} {
		p := newImagePickerLoading(pickerForTest(t), io.Discard, colorprofile.ASCII)
		p.signals = make(chan os.Signal, 1)
		p.signals <- test.signal
		called := false
		err := p.request(t.Context(), func(context.Context) error {
			called = true
			return nil
		})
		require.False(t, called)
		require.ErrorIs(t, err, context.Canceled)
		var interrupted *imagePickerGenerationInterrupt
		require.ErrorAs(t, err, &interrupted)
		require.Equal(t, test.code, interrupted.ExitCode())
		p.close()
	}
}

func TestImagePickerLoadingWorkerStagesResizeAndCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var out lockedLoadingBuffer
		m := pickerForTest(t)
		m.settings.prompt = "Synthetic horse"
		p := newImagePickerLoading(m, &out, colorprofile.ASCII)
		p.restoreWrap = true
		ctx := context.WithValue(t.Context(), imagePickerLoadingKey{}, p)
		width, height := 80, 24
		stop, advance := startLoadingFeedback(ctx, &out, "Generating image", m.settings.prompt, true, spinner.Line, func() (int, int) { return width, height })
		time.Sleep(imageLoadingDelay)
		synctest.Wait()
		frame := out.String()
		require.Contains(t, frame, "Create image")
		require.Contains(t, frame, "Generating image")
		require.NotContains(t, frame, "Command preview")
		require.Contains(t, frame, "Synthetic horse")
		require.Contains(t, frame, "[Ctrl+C]")
		advance(imageLoadingSaving)
		synctest.Wait()
		require.Contains(t, out.String(), "Saving image")
		width, height = 40, 12
		time.Sleep(time.Second)
		synctest.Wait()
		last := out.String()[strings.LastIndex(out.String(), "\r"+ansi.EraseScreenBelow):]
		require.Contains(t, last, "Create image")
		require.Contains(t, last, "[Ctrl+C]")
		advance(imageLoadingSaved)
		stop()
		require.True(t, strings.HasSuffix(out.String(), ansi.SetModeTextCursorEnable+ansi.ResetModeAutoWrap))
		_, err := io.WriteString(&out, "Saved image: synthetic.png\nPreview bytes\n")
		require.NoError(t, err)
		before := out.String()
		p.close()
		stop()
		require.Equal(t, before, out.String(), "late cleanup must not erase saved paths or previews")
	})
}

func TestImagePickerLoadingStopsBeforeDelayAndOnCancellation(t *testing.T) {
	for _, cancelFirst := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			var out bytes.Buffer
			m := pickerForTest(t)
			p := newImagePickerLoading(m, &out, colorprofile.ASCII)
			ctx, cancel := context.WithCancel(context.WithValue(t.Context(), imagePickerLoadingKey{}, p))
			defer cancel()
			stop, _ := startLoadingFeedback(ctx, &out, "Generating image", "", true, spinner.Line, func() (int, int) { return 80, 24 })
			if cancelFirst {
				cancel()
			}
			stop()
			require.Equal(t, "\r"+ansi.EraseScreenBelow+ansi.SetModeTextCursorEnable, out.String())
			before := out.String()
			time.Sleep(time.Second)
			synctest.Wait()
			require.Equal(t, before, out.String())
		})
	}
}
