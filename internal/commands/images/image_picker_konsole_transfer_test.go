package images

import (
	"context"
	"errors"
	"image"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/require"
)

func TestImagePickerKonsoleTransferCommitsAfterCompletion(t *testing.T) {
	m, w := konsoleWorkspaceForTest(t)
	w.expanded = true
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	before := append([]string(nil), w.painted...)
	messages := make(chan tea.Msg, 1)
	w.send = func(message tea.Msg) { messages <- message }
	t.Cleanup(func() { require.NoError(t, w.close()) })
	stageKonsoleImageForTest(t, m, w)
	require.NotNil(t, w.nativeTransfer)
	require.Equal(t, before, w.painted, "starting output cannot claim that an image is painted")
	require.Empty(t, w.nativePainted)
	select {
	case message := <-messages:
		completed, ok := message.(imagePickerNativeWritten)
		require.True(t, ok)
		require.NoError(t, w.finishNativeTransfer(completed.transfer))
	case <-time.After(3 * time.Second):
		t.Fatal("native transfer did not finish")
	}
	w.transferWorkers.Wait()
	require.Nil(t, w.nativeTransfer)
	require.Len(t, w.nativePainted, 1)
	require.Greater(t, w.nativeBytes, 0)
	require.LessOrEqual(t, w.nativeBytes, imagePickerNativeCacheBytes)
	output := konsoleOutputForTest(t, w)
	require.Contains(t, output, "\x1b[4;5H\x1b]1337;File=")
	require.NotContains(t, output, "    \x1b]1337;File=")
}

func TestImagePickerKonsoleTransferDefersReplacementWrites(t *testing.T) {
	m, w := konsoleWorkspaceForTest(t)
	w.expanded = true
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	before := konsoleOutputForTest(t, w)
	ctx, cancel := context.WithCancel(t.Context())
	transfer := &imagePickerNativeTransfer{context: ctx, cancel: cancel, replace: make(chan struct{}, 1), done: make(chan struct{}), index: w.selected, size: w.konsoleViewport}
	w.nativeTransfer = transfer
	p := &imagePickerInline{model: m, workspace: w}
	p.closeExpanded()
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	p.prepareExpanded()
	require.Equal(t, before, konsoleOutputForTest(t, w), "return controls must not enter an unfinished PNG")
	require.False(t, w.previewPending, "replacement preparation must not overlap the current transfer")
	select {
	case <-transfer.replace:
	default:
		t.Fatal("closing Expand did not request a safe transfer boundary")
	}
	transfer.replaced = true
	close(transfer.done)
	require.NoError(t, w.finishNativeTransfer(transfer))
	require.Empty(t, w.nativePainted)
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	require.Contains(t, konsoleOutputForTest(t, w)[len(before):], "\x1b[?1049l")
}

func TestImagePickerKonsoleReplacementTerminatesPartialPNG(t *testing.T) {
	_, w := konsoleWorkspaceForTest(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	transfer := &imagePickerNativeTransfer{context: ctx, replace: make(chan struct{}, 1)}
	transfer.replace <- struct{}{}
	_, err := w.out.WriteString("\x1b]1337;File=unfinished:")
	require.NoError(t, err)
	replaced, err := writeImagePickerNativeTransfer(transfer, w.out, []string{"must not continue the old image"})
	require.NoError(t, err)
	require.True(t, replaced)
	require.Equal(t, "\x1b]1337;File=unfinished:\x00\x1b\\", konsoleOutputForTest(t, w))
}

func TestImagePickerKonsoleCanceledTransferRetainsResetFailure(t *testing.T) {
	_, w := konsoleWorkspaceForTest(t)
	ctx, cancel := context.WithCancel(t.Context())
	transfer := &imagePickerNativeTransfer{context: ctx, cancel: cancel, replace: make(chan struct{}, 1), done: make(chan struct{})}
	w.nativeTransfer = transfer
	require.NoError(t, w.out.Close())
	cancel()
	transfer.replaced, transfer.err = writeImagePickerNativeTransfer(transfer, w.out, []string{"unwritten image"})
	close(transfer.done)
	err := w.stopNativeTransfer()
	require.ErrorIs(t, err, os.ErrClosed)
	require.NotErrorIs(t, err, context.Canceled)
}

func TestImagePickerKonsoleExpandedViewKeepsPayloadUncopied(t *testing.T) {
	m, w := konsoleWorkspaceForTest(t)
	w.expanded = true
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	rows, _, err := imagePickerKonsolePreview(t.Context(), w.out, image.NewNRGBA(image.Rect(0, 0, 32, 32)), "iterm-konsole", 8)
	require.NoError(t, err)
	w.expandedIndex, w.expandedSize = w.selected, w.konsoleViewport
	w.expandedNativeRows, w.expandedColumns = 1, 8
	w.stageNativeRows(w.selected, true, 8, rows)
	lines := w.expandedView(m, false)
	require.Equal(t, rows[0], lines[3])
	require.True(t, strings.HasPrefix(lines[3], "\x1b]1337;"))
}

func TestImagePickerKonsoleTransferKeepsQueuedRequestFailure(t *testing.T) {
	for _, requestFirst := range []bool{false, true} {
		name := "transfer first"
		if requestFirst {
			name = "request first"
		}
		t.Run(name, func(t *testing.T) {
			m, w := konsoleWorkspaceForTest(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			transferErr := errors.New("synthetic incomplete transfer")
			requestErr := errors.New("synthetic nonrecoverable request failure")
			transfer := &imagePickerNativeTransfer{context: ctx, cancel: cancel, done: make(chan struct{}), err: transferErr}
			close(transfer.done)
			w.nativeTransfer = transfer
			canceled := 0
			p := &imagePickerInline{model: m, workspace: w, requestCancel: func() {}, output: &imagePickerOutput{cancel: func() { canceled++ }}}
			messages := []tea.Msg{imagePickerNativeWritten{transfer}, imagePickerWorkspaceDone{err: requestErr}}
			if requestFirst {
				messages[0], messages[1] = messages[1], messages[0]
			}
			for _, message := range messages {
				handled, command := p.updateWorkspace(message)
				require.True(t, handled)
				require.NotNil(t, command)
			}
			require.ErrorIs(t, p.err, transferErr)
			require.ErrorIs(t, p.err, requestErr)
			require.Equal(t, 1, canceled, "transfer errors must stop queued painting immediately")
		})
	}
}
