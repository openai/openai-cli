package images

import (
	"context"
	"errors"
	"image"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-cli/internal/imageoutput"
	"github.com/stretchr/testify/require"
)

// Use the real viewport callers, but inject every font operation. A sized private
// PTY runs these tests without activating fonts or writing terminal graphics.
func fontQueuePicker(t *testing.T) (*imagePickerInline, *imagePickerWorkspace) {
	t.Helper()
	size, err := interactiveImageViewport(os.Stdout)
	if err != nil || size.width < 40 || size.height < 20 {
		t.Skip("requires stdout on a private PTY sized at least 40 columns by 20 rows")
	}
	for _, name := range []string{"CI", "SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY", "TMUX", "STY", "ZELLIJ", "KITTY_WINDOW_ID"} {
		t.Setenv(name, "")
	}
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	t.Setenv("TERM", "xterm-256color")
	w := &imagePickerWorkspace{out: os.Stdout, context: t.Context(), expanded: true, expandedIndex: -1}
	p := &imagePickerInline{
		model:     &imagePicker{width: size.width, height: size.height, workspace: true},
		workspace: w,
		output:    &imagePickerOutput{File: os.Stdout, cancel: func() {}},
	}
	return p, w
}

func TestImagePickerFontPreviewWaitsForSavedBatchPublication(t *testing.T) {
	p, w := fontQueuePicker(t)
	first, second := recoverySavedImage(t), recoverySavedImage(t)
	files := []imageoutput.SavedImage{first.file, second.file}
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		w.context = ctx
		messages := make(chan tea.Msg, 4)
		publishSecond := make(chan struct{})
		w.send = func(msg tea.Msg) {
			if entry, ok := msg.(imagePickerWorkspaceImage); ok && entry.file.Path == second.file.Path {
				select {
				case <-publishSecond:
				case <-ctx.Done():
					return
				}
			}
			select {
			case messages <- msg:
			case <-ctx.Done():
			}
		}
		secondStarted, finishSecond := make(chan struct{}), make(chan struct{})
		var calls atomic.Int32
		w.prepareRows = func(ctx context.Context, _ *os.File, _ image.Image, protocol string, _ int) ([]string, string, error) {
			if calls.Add(1) == 2 {
				close(secondStarted)
				select {
				case <-finishSecond:
				case <-ctx.Done():
					return nil, protocol, ctx.Err()
				}
			}
			return []string{"synthetic sharp rows"}, protocol, nil
		}
		done := make(chan error, 1)
		go func() { done <- w.saved(ctx, files, "on") }()
		arrival, ok := (<-messages).(imagePickerWorkspaceImage)
		require.True(t, ok)
		require.Equal(t, "font", arrival.protocol)
		w.appendImage(arrival)
		<-secondStarted
		p.prepareExpanded()
		synctest.Wait()
		require.Equal(t, int32(2), calls.Load(), "expansion must wait while the next batch image changes the font")
		close(finishSecond)
		synctest.Wait()
		require.Equal(t, int32(2), calls.Load(), "the batch owns the font through final image publication")
		close(publishSecond)
		last, ok := (<-messages).(imagePickerWorkspaceImage)
		require.True(t, ok)
		require.Equal(t, second.file, last.file)
		require.NoError(t, <-done)
		expanded, ok := (<-messages).(imagePickerExpandedRows)
		require.True(t, ok)
		w.previewWorkers.Wait()
		require.Equal(t, int32(3), calls.Load())
		require.Equal(t, 0, expanded.index)
		require.Equal(t, "font", expanded.protocol)
		require.Equal(t, []string{"synthetic sharp rows"}, expanded.rows)
		require.NoError(t, w.previewError)
	})
}

func TestImagePickerFontPreviewReplacedWhileQueued(t *testing.T) {
	p, w := fontQueuePicker(t)
	w.images = []imagePickerWorkspaceImage{recoverySavedImage(t), recoverySavedImage(t)}
	w.images[0].offset, w.images[1].offset = 7, 11
	synctest.Test(t, func(t *testing.T) {
		w.context = t.Context()
		messages := make(chan tea.Msg, 4)
		w.send = func(msg tea.Msg) { messages <- msg }
		var calls atomic.Int32
		w.prepareRows = func(_ context.Context, _ *os.File, _ image.Image, protocol string, _ int) ([]string, string, error) {
			calls.Add(1)
			return []string{"latest selection"}, protocol, nil
		}
		release, err := w.acquireFontPreview(t.Context())
		require.NoError(t, err)
		release = sync.OnceFunc(release)
		defer release()
		p.prepareExpanded()
		synctest.Wait()
		w.selected = 1
		p.prepareExpanded()
		synctest.Wait()
		require.Zero(t, calls.Load(), "queued previews must not prepare or change the font")
		release()
		synctest.Wait()
		w.previewWorkers.Wait()
		require.Equal(t, int32(1), calls.Load(), "the canceled selection must never prepare after acquiring the gate")
		require.Len(t, messages, 1)
		result, ok := (<-messages).(imagePickerExpandedRows)
		require.True(t, ok)
		require.Equal(t, 1, result.index)
		require.Equal(t, w.previewRevision, result.revision)
		require.Equal(t, []string{"latest selection"}, result.rows)
		require.NoError(t, w.previewError)
		require.Equal(t, 7, w.images[0].offset)
		require.Equal(t, 11, w.images[1].offset)
	})
}

func TestImagePickerFontPreviewCanceledWhileQueued(t *testing.T) {
	p, w := fontQueuePicker(t)
	w.images = []imagePickerWorkspaceImage{recoverySavedImage(t)}
	w.images[0].rows, w.images[0].offset = []string{"previous preview"}, 9
	w.expandedRows = []string{"previous expanded preview"}
	synctest.Test(t, func(t *testing.T) {
		w.context = t.Context()
		messages := make(chan tea.Msg, 4)
		w.send = func(msg tea.Msg) { messages <- msg }
		var calls atomic.Int32
		w.prepareRows = func(_ context.Context, _ *os.File, _ image.Image, protocol string, _ int) ([]string, string, error) {
			calls.Add(1)
			return []string{"unexpected replacement"}, protocol, nil
		}
		release, err := w.acquireFontPreview(t.Context())
		require.NoError(t, err)
		release = sync.OnceFunc(release)
		defer release()
		p.prepareExpanded()
		synctest.Wait()
		p.closeExpanded()
		synctest.Wait()
		w.previewWorkers.Wait()
		require.Zero(t, calls.Load())
		require.Empty(t, messages, "canceling a queued preview must not report a display failure")
		require.NoError(t, w.previewError)
		require.Equal(t, []string{"previous preview"}, w.images[0].rows)
		require.Equal(t, []string{"previous expanded preview"}, w.expandedRows)
		require.Equal(t, 9, w.images[0].offset)
		release()
		synctest.Wait()
		require.Zero(t, calls.Load(), "released capacity must not restart canceled work")
	})
}

func TestImagePickerFontPreviewCanceledSavedWaitKeepsReceipts(t *testing.T) {
	_, w := fontQueuePicker(t)
	files := []imageoutput.SavedImage{recoverySavedImage(t).file, recoverySavedImage(t).file}
	synctest.Test(t, func(t *testing.T) {
		w.context = t.Context()
		messages := make(chan tea.Msg, 4)
		w.send = func(msg tea.Msg) { messages <- msg }
		var calls atomic.Int32
		w.prepareRows = func(_ context.Context, _ *os.File, _ image.Image, protocol string, _ int) ([]string, string, error) {
			calls.Add(1)
			return []string{"saved preview"}, protocol, nil
		}
		release, err := w.acquireFontPreview(t.Context())
		require.NoError(t, err)
		release = sync.OnceFunc(release)
		defer release()
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- w.saved(ctx, files, "on") }()
		synctest.Wait()
		cancel()
		synctest.Wait()
		require.ErrorIs(t, <-done, context.Canceled)
		require.Zero(t, calls.Load())
		require.Empty(t, messages)
		require.Equal(t, []string{files[0].Path, files[1].Path}, w.receipts, "saved originals need receipts even if preview never starts")
		release()
		require.NoError(t, w.saved(t.Context(), files[:1], "on"), "canceling the waiter must not consume the font gate")
		require.Equal(t, int32(1), calls.Load())
		require.Len(t, messages, 1)
	})
}

func TestImagePickerFontPreviewErrorReleasesBatchAndPreviewQueue(t *testing.T) {
	for _, failingWorker := range []string{"saved", "expanded"} {
		t.Run(failingWorker, func(t *testing.T) {
			p, w := fontQueuePicker(t)
			entry := recoverySavedImage(t)
			w.images = []imagePickerWorkspaceImage{entry}
			synctest.Test(t, func(t *testing.T) {
				w.context = t.Context()
				messages := make(chan tea.Msg, 4)
				w.send = func(msg tea.Msg) { messages <- msg }
				failure := errors.New("synthetic font rollback failure")
				var calls atomic.Int32
				w.prepareRows = func(_ context.Context, _ *os.File, _ image.Image, protocol string, _ int) ([]string, string, error) {
					if calls.Add(1) == 1 {
						return nil, protocol, failure
					}
					return []string{"recovered preparation"}, protocol, nil
				}
				if failingWorker == "saved" {
					require.ErrorIs(t, w.saved(t.Context(), []imageoutput.SavedImage{entry.file}, "on"), failure)
					require.Equal(t, []string{entry.file.Path}, w.receipts)
					p.prepareExpanded()
					synctest.Wait()
					w.previewWorkers.Wait()
					result, ok := (<-messages).(imagePickerExpandedRows)
					require.True(t, ok)
					require.Equal(t, []string{"recovered preparation"}, result.rows)
				} else {
					p.prepareExpanded()
					synctest.Wait()
					w.previewWorkers.Wait()
					result, ok := (<-messages).(imagePickerPreviewFailure)
					require.True(t, ok)
					require.ErrorIs(t, result.err, failure)
					require.ErrorIs(t, w.previewError, failure, "unsafe rollback errors must remain observable")
					require.NoError(t, w.saved(t.Context(), []imageoutput.SavedImage{entry.file}, "on"))
					resultImage, ok := (<-messages).(imagePickerWorkspaceImage)
					require.True(t, ok)
					require.Equal(t, []string{"recovered preparation"}, resultImage.rows)
				}
				require.Equal(t, int32(2), calls.Load())
			})
		})
	}
}

func TestImagePickerFontPreviewQueueDoesNotBlockOtherProtocols(t *testing.T) {
	p, w := fontQueuePicker(t)
	entry := recoverySavedImage(t)
	entry.protocol = "kitty"
	w.images = []imagePickerWorkspaceImage{entry}
	synctest.Test(t, func(t *testing.T) {
		w.context = t.Context()
		messages := make(chan tea.Msg, 1)
		w.send = func(msg tea.Msg) { messages <- msg }
		w.prepareRows = func(_ context.Context, _ *os.File, _ image.Image, protocol string, _ int) ([]string, string, error) {
			return []string{"synthetic kitty rows"}, protocol, nil
		}
		release, err := w.acquireFontPreview(t.Context())
		require.NoError(t, err)
		release = sync.OnceFunc(release)
		defer release()
		p.prepareExpanded()
		synctest.Wait()
		require.Len(t, messages, 1, "independent image protocols must not wait for the font resource")
		result, ok := (<-messages).(imagePickerExpandedRows)
		require.True(t, ok)
		require.Equal(t, "kitty", result.protocol)
		w.previewWorkers.Wait()
		release()
	})
}
