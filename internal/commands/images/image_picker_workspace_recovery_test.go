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
	"strings"
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
		require.Contains(t, strings.Join(w.painted, "\n"), row)
		m.width = 40
		require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
		require.NotContains(t, strings.Join(w.painted, "\n"), row)
		w.expanded = true
		require.NoError(t, w.draw(m, colorprofile.ASCII, false, false))
		require.NotContains(t, strings.Join(w.painted, "\n"), row, "expanded fallback must also respect the smaller viewport")
		require.Equal(t, row, w.images[0].rows[0], "resizing must retain the original cached preview")
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
		_, _, _, err := imagePickerPrepareResize(ctx, nil, entry, interactiveImageSize{80, 28, 8, 16}, true, prepare)
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
	w.replaceImageRows(0, []string{"resized"}, 16, "blocks")
	require.Equal(t, 16, w.images[0].columns)
	require.Equal(t, "other image", w.rows[w.images[1].start+2])
	require.Equal(t, "retained diagnostic", w.rows[len(w.rows)-1])
}

func TestImagePickerResizeCompletionClearsPendingAfterSelectionChanges(t *testing.T) {
	w := &imagePickerWorkspace{previewRevision: 3, previewPending: true, selected: 1, images: []imagePickerWorkspaceImage{{}, {}}}
	p := &imagePickerInline{workspace: w}
	handled, _ := p.updateWorkspace(imagePickerExpandedRows{revision: 3, index: 0, rows: []string{"resized"}, inline: true})
	require.True(t, handled)
	require.False(t, w.previewPending, "the completed worker cannot remain pending after selection changes")
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
