package images

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/openai/openai-cli/internal/imageoutput"
	"github.com/openai/openai-cli/internal/terminalimage"
	"github.com/stretchr/testify/require"
)

func TestImagePickerWorkspaceResizeDoesNotReplayOversizedNativeRows(t *testing.T) {
	for _, row := range []string{"\x1b]1337;File=inline=1;width=64;height=1:AA==\a", "\x1b_Ga=T,c=64,r=1;AA==\x1b\\"} {
		out, err := os.CreateTemp(t.TempDir(), "screen")
		require.NoError(t, err)
		t.Cleanup(func() { out.Close() })
		m := pickerForTest(t)
		m.width, m.height, m.workspace, m.hasImages = 96, 28, true, true
		w := &imagePickerWorkspace{out: out}
		w.appendImage(imagePickerWorkspaceImage{protocol: "iterm", columns: 64, rows: []string{row}})
		require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
		output, err := os.ReadFile(out.Name())
		require.NoError(t, err)
		require.Contains(t, string(output), row)
		written := len(output)
		m.width = 40
		require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
		output, err = os.ReadFile(out.Name())
		require.NoError(t, err)
		require.NotContains(t, string(output[written:]), row)
		written = len(output)
		w.expanded = true
		require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
		output, err = os.ReadFile(out.Name())
		require.NoError(t, err)
		require.NotContains(t, string(output[written:]), row, "expanded fallback must also respect the smaller viewport")
		cached, ok := w.nativePayload(imagePickerNativeRow{0, 0, 64, 1, false})
		require.True(t, ok)
		require.Equal(t, row, cached, "resizing may retain the original bounded cache entry")
	}
}

func recoverySavedImage(t *testing.T) imagePickerWorkspaceImage {
	t.Helper()
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 32, 32))))
	path := filepath.Join(t.TempDir(), "synthetic.png")
	require.NoError(t, os.WriteFile(path, encoded.Bytes(), 0600))
	return imagePickerWorkspaceImage{file: imageoutput.SavedImage{Path: path, SHA256: sha256.Sum256(encoded.Bytes())}, protocol: "font"}
}

func TestImagePickerResizePreparationPreservesUnsafeFontFailure(t *testing.T) {
	for _, cancelFirst := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		entry := recoverySavedImage(t)
		rollback := errors.New("synthetic font rollback failed")
		prepare := func(context.Context, *os.File, image.Image, string, int) ([]string, string, error) {
			if cancelFirst {
				cancel()
				return nil, "font", errors.Join(context.Canceled, rollback)
			}
			return nil, "font", rollback
		}
		_, err := imagePickerPrepareResize(ctx, nil, entry, interactiveImageSize{80, 28, 8, 16}, true, prepare)
		require.ErrorIs(t, err, rollback)
		require.False(t, imagePickerSafePreviewFailure(err))
		if cancelFirst {
			require.ErrorIs(t, err, context.Canceled)
		}
	}
	safe := &terminalimage.FontError{Err: errors.New("synthetic safe rollback")}
	require.True(t, imagePickerSafePreviewFailure(safe))
	require.True(t, imagePickerSafePreviewFailure(context.Canceled))
	require.False(t, imagePickerSafePreviewFailure(errors.Join(safe, errors.New("another failure"))))
}

func TestImagePickerShutdownRetainsUnconsumedRequestFailure(t *testing.T) {
	for _, unsafe := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		p := &imagePickerInline{requestCancel: cancel, requestDone: done}
		rollback := errors.New("synthetic rollback failure after UI shutdown")
		go func() {
			<-ctx.Done()
			p.requestErr = context.Canceled
			if unsafe {
				p.requestErr = errors.Join(p.requestErr, rollback)
			}
			close(done)
		}()
		p.stopWorkspaceRequest()
		if unsafe {
			require.ErrorIs(t, p.err, rollback)
			require.ErrorIs(t, p.err, context.Canceled)
		} else {
			require.NoError(t, p.err)
		}
	}
}

func TestImagePickerCloseRetainsUnconsumedPreviewFailure(t *testing.T) {
	failure := errors.New("synthetic failed restore")
	w := &imagePickerWorkspace{previewError: errors.Join(context.Canceled, failure)}
	require.ErrorIs(t, w.close(), failure)
}

func TestImagePickerReplacingResizedRowsPreservesLaterImages(t *testing.T) {
	w := &imagePickerWorkspace{}
	w.appendImage(imagePickerWorkspaceImage{rows: []string{"old one", "old two"}})
	w.appendImage(imagePickerWorkspaceImage{rows: []string{"other image"}})
	w.rows = append(w.rows, "retained diagnostic")
	w.replaceImageRows(imagePickerExpandedRows{index: 0, rows: []string{"resized"}, columns: 16, protocol: "blocks"})
	require.Equal(t, 16, w.images[0].columns)
	require.Equal(t, "other image", w.images[1].rows[0])
	require.Equal(t, "retained diagnostic", w.rows[len(w.rows)-1])
}

func TestImagePickerResizeCompletionClearsPendingAfterSelectionChanges(t *testing.T) {
	m, w := browserForTest(t, 80, 28)
	w.previewRevision, w.previewPending, w.selected = 3, true, 1
	w.images = []imagePickerWorkspaceImage{{}, {}}
	p := &imagePickerInline{workspace: w, model: m}
	handled, _ := p.updateWorkspace(imagePickerExpandedRows{revision: 3, index: 0, rows: []string{"resized"}, inline: true})
	require.True(t, handled)
	require.False(t, w.previewPending, "the completed worker cannot remain pending after selection changes")
	require.Equal(t, []string{"resized"}, w.images[0].rows, "visible background previews must accept their current worker result")
}

func TestImagePickerBackCancelsPendingPreviewForReentry(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	w := &imagePickerWorkspace{previewPending: true, expanded: true, previewCancel: cancel}
	p := &imagePickerInline{workspace: w}
	p.closeExpanded()
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.False(t, w.previewPending, "returning to this image must permit a new worker")
	require.False(t, w.expanded)
}

func TestImagePickerInlineResizeRecoversAfterUnavailableViewport(t *testing.T) {
	for name, small := range map[string]interactiveImageSize{
		"width":  {8, 28, 8, 16},
		"height": {40, 4, 8, 16},
	} {
		t.Run(name, func(t *testing.T) {
			entry := recoverySavedImage(t)
			entry.rows, entry.columns, entry.inlineOffset = []string{"cached pixels"}, 64, 7
			w := &imagePickerWorkspace{images: []imagePickerWorkspaceImage{entry}}
			calls := 0
			prepare := func(context.Context, *os.File, image.Image, string, int) ([]string, string, error) {
				calls++
				return []string{"recovered pixels"}, "font", nil
			}
			result, err := imagePickerPrepareResize(t.Context(), nil, entry, small, false, prepare)
			require.NoError(t, err)
			result.size = small
			w.replaceImageRows(result)
			require.Zero(t, calls, "a viewport that cannot fit pixels must not invoke the encoder")
			require.Equal(t, entry.rows, w.images[0].rows)
			require.Equal(t, entry.columns, w.images[0].columns)
			require.Equal(t, entry.inlineOffset, w.images[0].inlineOffset)
			for range 3 {
				require.Equal(t, -1, w.inlineResizeIndex(small), "progress ticks must not retry the same unusable viewport")
			}
			large := small
			if name == "width" {
				large.width = 96
			} else {
				large.height = 28
			}
			require.Equal(t, 0, w.inlineResizeIndex(large), "enlarging either dimension must retry the saved image")
			result, err = imagePickerPrepareResize(t.Context(), nil, w.images[0], large, false, prepare)
			require.NoError(t, err)
			w.replaceImageRows(result)
			require.Equal(t, 1, calls)
			require.Equal(t, []string{"recovered pixels"}, w.images[0].rows)
			require.Nil(t, w.images[0].unavailableSize)
			require.Equal(t, -1, w.inlineResizeIndex(large), "a recovered preview must not keep encoding")
		})
	}
}

func TestImagePickerUnavailableViewportDoesNotBlockOtherImages(t *testing.T) {
	size := interactiveImageSize{40, 12, 8, 16}
	w := &imagePickerWorkspace{images: []imagePickerWorkspaceImage{
		{unavailableSize: &size, rows: []string{"cached"}, columns: 64},
		{rows: []string{"other"}, columns: 64},
	}, galleryStarts: []int{0, 2}, displayRows: make([]string, 4), viewportRows: 4}
	require.Equal(t, 1, w.inlineResizeIndex(size))
	changed := size
	changed.cellHeight++
	require.Equal(t, 0, w.inlineResizeIndex(changed), "changed cell geometry can make a preview fit")
}

func TestImagePickerFailedPreviewDoesNotRetryAsViewportRecovery(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		entry := recoverySavedImage(t)
		if disabled {
			entry.protocol = ""
		} else {
			require.NoError(t, os.Remove(entry.file.Path))
		}
		size := interactiveImageSize{80, 28, 8, 16}
		result, err := imagePickerPrepareResize(t.Context(), nil, entry, size, false, nil)
		require.NoError(t, err)
		w := &imagePickerWorkspace{images: []imagePickerWorkspaceImage{entry}}
		w.replaceImageRows(result)
		require.Equal(t, -1, w.inlineResizeIndex(size))
		size.width++
		require.Equal(t, -1, w.inlineResizeIndex(size), "disabled or unreadable files must retain their existing failure behavior")
	}
}

func TestImagePickerStaleViewportFailureCannotBlockCurrentPreview(t *testing.T) {
	w := &imagePickerWorkspace{previewRevision: 4, images: []imagePickerWorkspaceImage{{rows: []string{"current pixels"}, columns: 32}}}
	p := &imagePickerInline{workspace: w}
	handled, _ := p.updateWorkspace(imagePickerExpandedRows{
		revision: 3, index: 0, inline: true, viewportUnavailable: true,
		size: interactiveImageSize{8, 28, 8, 16},
	})
	require.True(t, handled)
	require.Nil(t, w.images[0].unavailableSize)
	require.Equal(t, []string{"current pixels"}, w.images[0].rows)
}
