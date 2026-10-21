package custom

import (
	"io"
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// Model terminals that ignore optional private-mode save/restore operations.
func applyPickerMouseModes(state map[int]bool, stream string) {
	pattern := regexp.MustCompile("\x1b\\[\\?([0-9]+)([hl])")
	for _, match := range pattern.FindAllStringSubmatch(stream, -1) {
		mode, _ := strconv.Atoi(match[1])
		state[mode] = match[2] == "h"
	}
}

func TestImagePickerMouseModesRestoreOnlyOwnedState(t *testing.T) {
	for _, test := range []struct {
		name    string
		values  [4]ansi.ModeSetting
		enabled bool
	}{
		{"unknown", [4]ansi.ModeSetting{}, false},
		{"partial", [4]ansi.ModeSetting{ansi.ModeReset, ansi.ModeReset, ansi.ModeReset}, false},
		{"ordinary shell", [4]ansi.ModeSetting{ansi.ModeReset, ansi.ModeReset, ansi.ModeReset, ansi.ModeReset}, true},
		{"inherited encoding", [4]ansi.ModeSetting{ansi.ModeReset, ansi.ModeReset, ansi.ModeReset, ansi.ModeSet}, true},
		{"inherited tracking", [4]ansi.ModeSetting{ansi.ModeSet, ansi.ModeReset, ansi.ModeReset, ansi.ModeSet}, false},
		{"inherited drag", [4]ansi.ModeSetting{ansi.ModeReset, ansi.ModeSet, ansi.ModeReset, ansi.ModeSet}, false},
		{"inherited motion", [4]ansi.ModeSetting{ansi.ModeReset, ansi.ModeReset, ansi.ModePermanentlySet, ansi.ModeSet}, false},
		{"encoding prohibited", [4]ansi.ModeSetting{ansi.ModeReset, ansi.ModeReset, ansi.ModeReset, ansi.ModePermanentlyReset}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := map[int]bool{1000: false, 1002: false, 1003: false, 1006: false, 2004: true, 7: true}
			modes := []ansi.DECMode{1000, 1002, 1003, 1006}
			var mouse imagePickerMouseModes
			for i, mode := range modes {
				state[int(mode)] = test.values[i].IsSet()
				require.True(t, mouse.report(tea.ModeReportMsg{Mode: mode, Value: test.values[i]}))
			}
			original := map[int]bool{}
			for mode, value := range state {
				original[mode] = value
			}
			frame := mouse.enable()
			require.Equal(t, test.enabled, frame != "")
			applyPickerMouseModes(state, frame)
			if test.enabled {
				require.True(t, state[1000])
				require.True(t, state[1006])
			}
			applyPickerMouseModes(state, mouse.disable())
			require.Equal(t, original, state)
			applyPickerMouseModes(state, mouse.disable())
			require.Equal(t, original, state)
			require.Empty(t, mouse.enable(), "late frames cannot reactivate reporting")
		})
	}
}

func TestImagePickerMouseCleanupPrecedesQuitAndWorkerCleanup(t *testing.T) {
	for _, msg := range []tea.Msg{tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, imagePickerStopMsg{code: 143}} {
		out, err := os.CreateTemp(t.TempDir(), "screen")
		require.NoError(t, err)
		t.Cleanup(func() { out.Close() })
		w := &imagePickerWorkspace{out: out}
		for _, mode := range []ansi.DECMode{1000, 1002, 1003, 1006} {
			w.mouse.report(tea.ModeReportMsg{Mode: mode, Value: ansi.ModeReset})
		}
		state := map[int]bool{1000: false, 1006: false}
		applyPickerMouseModes(state, w.mouse.enable())
		require.True(t, state[1000])
		p := &imagePickerInline{model: pickerForTest(t), workspace: w, started: true}
		p.Update(msg)
		require.NoError(t, p.err)
		_, err = out.Seek(0, io.SeekStart)
		require.NoError(t, err)
		raw, err := io.ReadAll(out)
		require.NoError(t, err)
		applyPickerMouseModes(state, string(raw))
		require.False(t, state[1000])
		require.False(t, state[1006])
		require.Empty(t, w.mouse.enable())
	}
	out, err := os.CreateTemp(t.TempDir(), "closed-screen")
	require.NoError(t, err)
	w := &imagePickerWorkspace{out: out}
	w.mouse.tracking = true
	require.NoError(t, out.Close())
	require.ErrorIs(t, w.stopMouse(), os.ErrClosed)
	require.True(t, w.mouse.stopped)
	require.Error(t, w.close(), "cleanup errors must remain observable")
}

func TestImagePickerWorkspaceExpandedFallbackPersistsProtocol(t *testing.T) {
	w := &imagePickerWorkspace{previewRevision: 4, images: []imagePickerWorkspaceImage{{protocol: "font"}}}
	p := &imagePickerInline{workspace: w}
	p.updateWorkspace(imagePickerExpandedRows{revision: 4, index: 0, rows: []string{"fallback"}, protocol: "blocks"})
	require.Equal(t, "blocks", w.images[0].protocol)
	p.updateWorkspace(imagePickerExpandedRows{revision: 3, index: 0, protocol: "font"})
	require.Equal(t, "blocks", w.images[0].protocol)
}

func TestImagePickerMouseStopsBeforeWaitingForPreviewWorkers(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "screen")
	require.NoError(t, err)
	t.Cleanup(func() { out.Close() })
	w := &imagePickerWorkspace{out: out}
	w.mouse.tracking, w.mouse.encoding = true, true
	w.previewWorkers.Add(1)
	released := false
	defer func() {
		if !released {
			w.previewWorkers.Done()
		}
	}()
	done := make(chan error, 1)
	go func() { done <- w.close() }()
	require.Eventually(t, func() bool { info, err := out.Stat(); return err == nil && info.Size() > 0 }, time.Second, time.Millisecond)
	select {
	case <-done:
		t.Fatal("cleanup did not wait for the held preview worker")
	default:
	}
	state := map[int]bool{1000: true, 1006: true}
	raw, err := os.ReadFile(out.Name())
	require.NoError(t, err)
	applyPickerMouseModes(state, string(raw))
	require.False(t, state[1000])
	require.False(t, state[1006])
	w.previewWorkers.Done()
	released = true
	require.NoError(t, <-done)
}
