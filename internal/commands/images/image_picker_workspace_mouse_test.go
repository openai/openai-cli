package images

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

func TestImagePickerMouseShellEnvironment(t *testing.T) {
	for _, test := range []struct {
		name, key, value string
		enabled          bool
	}{
		{"native shell", "", "", true},
		{"other terminal", "TERM_PROGRAM", "iTerm.app", false},
		{"kitty", "KITTY_WINDOW_ID", "synthetic", false},
		{"ssh connection", "SSH_CONNECTION", "synthetic", false},
		{"ssh client", "SSH_CLIENT", "synthetic", false},
		{"ssh tty", "SSH_TTY", "/dev/synthetic", false},
		{"tmux", "TMUX", "synthetic", false},
		{"screen", "STY", "synthetic", false},
		{"zellij", "ZELLIJ", "synthetic", false},
		{"ci true", "CI", "true", false},
		{"ci one", "CI", "1", false},
		{"ci zero", "CI", "0", true},
		{"ci false", "CI", "FALSE", true},
		{"screen term", "TERM", "screen-256color", false},
		{"tmux term", "TERM", "tmux-256color", false},
		{"dumb", "TERM", "dumb", false},
		{"missing term", "TERM", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := map[string]string{"TERM_PROGRAM": "Apple_Terminal", "TERM": "xterm-256color"}
			env[test.key] = test.value
			require.Equal(t, test.enabled, imagePickerMouseShellEnvironment(func(key string) string { return env[key] }))
		})
	}
}

func TestImagePickerMouseShellRecoversTracking(t *testing.T) {
	for _, inherited := range []int{0, 1000, 1002, 1003, 1006} {
		t.Run(strconv.Itoa(inherited), func(t *testing.T) {
			state := map[int]bool{1000: false, 1002: false, 1003: false, 1006: false, 2004: true, 7: true}
			if inherited != 0 {
				state[inherited], state[1006] = true, true
			}
			var mouse imagePickerMouseModes
			reset := mouse.openShell()
			require.True(t, mouse.shell, "ownership precedes the attempted write")
			applyPickerMouseModes(state, reset)
			for _, mode := range []int{1000, 1002, 1003, 1006} {
				require.False(t, state[mode])
			}
			require.Empty(t, mouse.openShell(), "late initialization cannot reset an active gallery")
			// Late replies cannot replace the owned disabled baseline.
			mouse.report(tea.ModeReportMsg{Mode: ansi.DECMode(1003), Value: ansi.ModeSet})
			applyPickerMouseModes(state, mouse.enable())
			require.True(t, state[1000])
			require.True(t, state[1006])
			for range 2 {
				applyPickerMouseModes(state, mouse.disable())
				for _, mode := range []int{1000, 1002, 1003, 1006} {
					require.False(t, state[mode])
				}
			}
			require.True(t, state[2004])
			require.True(t, state[7])
			require.Empty(t, mouse.enable(), "late worker frames cannot enable reporting")
			require.Empty(t, mouse.openShell(), "late initialization cannot reopen reporting")
		})
	}
}

func TestImagePickerMouseShellInitSkipsQueries(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	t.Setenv("KITTY_WINDOW_ID", "")
	out, err := os.CreateTemp(t.TempDir(), "screen")
	require.NoError(t, err)
	t.Cleanup(func() { out.Close() })
	w := &imagePickerWorkspace{out: out}
	p := &imagePickerInline{model: pickerForTest(t), workspace: w, mouseShell: true, output: &imagePickerOutput{File: out, cancel: func() {}}}
	p.Init()
	_, err = out.Seek(0, io.SeekStart)
	require.NoError(t, err)
	raw, err := io.ReadAll(out)
	require.NoError(t, err)
	require.Contains(t, string(raw), imagePickerMouseShellReset)
	require.NotContains(t, string(raw), "$p")
	require.True(t, w.mouse.shell)
	require.NoError(t, w.stopMouse())
}

func TestImagePickerMouseShellRejectsMissingTerminal(t *testing.T) {
	require.False(t, imagePickerNativeMouseShell(t.Context(), nil, nil))
	out, err := os.CreateTemp(t.TempDir(), "ordinary-file")
	require.NoError(t, err)
	t.Cleanup(func() { out.Close() })
	require.False(t, imagePickerNativeMouseShell(t.Context(), out, out))
}

func TestImagePickerMouseShellStopsBeforeQuit(t *testing.T) {
	for _, msg := range []tea.Msg{tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, imagePickerStopMsg{code: 143}} {
		out, err := os.CreateTemp(t.TempDir(), "screen")
		require.NoError(t, err)
		t.Cleanup(func() { out.Close() })
		w := &imagePickerWorkspace{out: out}
		w.mouse.openShell()
		w.mouse.enable()
		p := &imagePickerInline{model: pickerForTest(t), workspace: w, started: true}
		p.Update(msg)
		require.NoError(t, p.err)
		raw, err := os.ReadFile(out.Name())
		require.NoError(t, err)
		require.Contains(t, string(raw), imagePickerMouseShellReset)
		require.Empty(t, w.mouse.enable())
	}
}

func TestImagePickerMouseShellFailedResetRemainsObservable(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "closed-screen")
	require.NoError(t, err)
	w := &imagePickerWorkspace{out: out}
	w.mouse.openShell()
	require.NoError(t, out.Close())
	require.ErrorIs(t, w.stopMouse(), os.ErrClosed)
	require.True(t, w.mouse.stopped)
	require.Equal(t, imagePickerMouseShellReset, w.mouse.disable(), "retry all owned resets")
	require.Empty(t, w.mouse.enable())
	require.Error(t, w.close())
}

func TestImagePickerMouseShellStopsBeforeWaitingForWorkers(t *testing.T) {
	for _, kind := range []string{"open", "preview"} {
		t.Run(kind, func(t *testing.T) {
			out, err := os.CreateTemp(t.TempDir(), "screen")
			require.NoError(t, err)
			t.Cleanup(func() { out.Close() })
			w := &imagePickerWorkspace{out: out}
			w.mouse.openShell()
			workers := &w.previewWorkers
			if kind == "open" {
				workers = &w.openWorkers
			}
			workers.Add(1)
			done := make(chan error, 1)
			finished := make(chan struct{})
			released := false
			defer func() {
				if !released {
					workers.Done()
				}
				<-finished
			}()
			go func() {
				defer close(finished)
				done <- w.close()
			}()
			require.Eventually(t, func() bool { info, err := out.Stat(); return err == nil && info.Size() > 0 }, time.Second, time.Millisecond)
			select {
			case <-finished:
				t.Fatal("cleanup did not wait for the held worker")
			default:
			}
			state := map[int]bool{1000: true, 1002: true, 1003: true, 1006: true}
			raw, err := os.ReadFile(out.Name())
			require.NoError(t, err)
			applyPickerMouseModes(state, string(raw))
			for _, mode := range []int{1000, 1002, 1003, 1006} {
				require.False(t, state[mode])
			}
			workers.Done()
			released = true
			require.NoError(t, <-done)
		})
	}
}
