package images

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/require"
)

func TestImagePickerITermReplacementPreservesPendingCursorQuery(t *testing.T) {
	m, w := browserForTest(t, 120, 60)
	m.hasImages, w.iterm = false, true
	p := &imagePickerInline{model: m, workspace: w, profile: colorprofile.ASCII}
	require.NoError(t, w.draw(m, p.profile, false, true))
	require.True(t, w.originQueryPending)
	original := w.originQueryRevision
	ctx, cancel := context.WithCancel(t.Context())
	transfer := &imagePickerNativeTransfer{context: ctx, cancel: cancel, inlineColumn: 55,
		size: interactiveImageSize{width: m.width, height: m.height}, replace: make(chan struct{}, 1), done: make(chan struct{})}
	w.nativeTransfer = transfer
	m.width--
	require.NoError(t, w.draw(m, p.profile, false, true))
	require.Len(t, transfer.replace, 1)
	transfer.replaced = true
	close(transfer.done)
	p.updateWorkspace(imagePickerNativeWritten{transfer})
	require.Greater(t, w.frameRevision, original)
	p.updateWorkspace(tea.CursorPositionMsg{Y: 8})
	require.False(t, w.originKnown)
	require.True(t, w.originQueryPending)
	p.updateWorkspace(tea.CursorPositionMsg{Y: 4})
	require.True(t, w.originKnown)
	require.Equal(t, 4, w.origin)
}

// A terminal can stop consuming a native frame while waiting for display
// consent. Input processing and resize must remain available during the wait.
func TestImagePickerITermTransferKeepsEventLoopAvailable(t *testing.T) {
	m, w := browserForTest(t, 120, 32)
	w.iterm = true
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	before := append([]string(nil), w.painted...)
	r, out, err := os.Pipe()
	require.NoError(t, err)
	defer r.Close()
	defer out.Close()
	w.out = out
	messages := make(chan tea.Msg, 1)
	w.send = func(msg tea.Msg) { messages <- msg }
	row := nativeCacheTestRow("iterm", 32, 1<<20)
	w.appendImage(imagePickerWorkspaceImage{protocol: "iterm", columns: 32, rows: []string{row}})
	started := make(chan error, 1)
	go func() { started <- w.draw(m, colorprofile.ASCII, false, true) }()
	select {
	case err := <-started:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("waiting for iTerm2 output blocked the event loop")
	}
	require.NotNil(t, w.nativeTransfer)
	require.Equal(t, before, w.painted)
	// Wait for the actual image header, leaving part of the image in flight.
	var first strings.Builder
	var chunk [4096]byte
	for !strings.Contains(first.String(), "\x1b]1337;File=") {
		n, err := r.Read(chunk[:])
		require.NoError(t, err)
		first.Write(chunk[:n])
	}
	m.width--
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	drained := make(chan string, 1)
	go func() {
		tail, _ := io.ReadAll(r)
		drained <- string(tail)
	}()
	select {
	case msg := <-messages:
		transfer := msg.(imagePickerNativeWritten).transfer
		require.True(t, transfer.replaced)
		require.NoError(t, w.finishNativeTransfer(transfer))
	case <-time.After(3 * time.Second):
		t.Fatal("resize failed to replace the in-flight transfer")
	}
	require.Equal(t, before, w.painted, "retain the cursor anchor for the next repaint")
	require.Empty(t, w.nativePainted)
	require.Zero(t, w.nativeColumn)
	require.NoError(t, out.Close())
	require.Contains(t, <-drained, "\x00\x1b\\")
	w.transferWorkers.Wait()
}
