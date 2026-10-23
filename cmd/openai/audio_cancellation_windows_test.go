//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// The controller keeps its Job Object alive until the parent checks production cleanup.
// The synthetic player registers console interrupts, so signal delivery cannot terminate it.
func TestMainAudioWorkbenchWindowsPlayerCancellationStatusAndCleanup(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event uint32
	}{{"CTRL_C", windows.CTRL_C_EVENT}, {"CTRL_BREAK", windows.CTRL_BREAK_EVENT}} {
		t.Run(tc.name, func(t *testing.T) {
			env, store, entry, preview, original := audioWorkbenchHistory(t)
			programs, receiptPath := prepareAudioWorkbenchHelper(t, true)
			originalFiles := make(map[string][]byte)
			for _, id := range []string{entry.ID, preview.ID} {
				for _, name := range []string{"audio", "manifest.json"} {
					path := filepath.Join(store.Directory, id, name)
					data, err := os.ReadFile(path)
					require.NoError(t, err)
					originalFiles[path] = data
				}
			}
			binary, err := os.Executable()
			require.NoError(t, err)
			control := t.TempDir()
			resultPath := filepath.Join(control, "console-result.json")
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			process := exec.CommandContext(ctx, binary, "-test.run=^TestShellFileWindowsConsoleHelper$", "--",
				binary, "-test.run=^TestMainDispatchProcess$", "--", "openai", "--format-error", "json", "audio", "play", entry.ID)
			process.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
			process.WaitDelay = 2 * time.Second
			for _, name := range []string{"SystemRoot", "WINDIR", "PATHEXT"} {
				if value, ok := os.LookupEnv(name); ok {
					env = append(env, name+"="+value)
				}
			}
			process.Env = append(env, "PATH="+programs, "TEMP="+control, "TMP="+control,
				"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_CLI_TEST_CONSOLE_RESULT="+resultPath,
				"OPENAI_CLI_TEST_CONSOLE_ACK=1", "GOMAXPROCS=2", "NO_COLOR=1", "TERM=dumb",
				"OPENAI_BASE_URL=://synthetic-private-endpoint", "OPENAI_API_KEY=sk-fake-audio-playback",
				"OPENAI_ADMIN_KEY=sk-fake-audio-admin", "OPENAI_WEBHOOK_SECRET=whsec-fake-audio",
				"OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic-private/missing-cert", "FFREPORT=synthetic-private-report")
			var stdout, stderr adminConsoleCapture
			process.Stdout, process.Stderr = &stdout, &stderr
			input, err := process.StdinPipe()
			require.NoError(t, err)
			started := false
			waitDone := make(chan struct{})
			var waitErr error
			var controllerHandle, playerHandle windows.Handle
			t.Cleanup(func() {
				input.Close() // EOF releases either controller input gate after an assertion failure.
				cancel()
				if started {
					select {
					case <-waitDone:
					case <-time.After(4 * time.Second):
						process.Process.Kill()
						select {
						case <-waitDone:
						case <-time.After(3 * time.Second):
							t.Error("audio console controller did not join during cleanup")
						}
					}
				}
				if playerHandle != 0 {
					state, err := windows.WaitForSingleObject(playerHandle, 0)
					if err != nil || state != windows.WAIT_OBJECT_0 {
						windows.TerminateProcess(playerHandle, 125)
						if state, err := windows.WaitForSingleObject(playerHandle, 3000); err != nil || state != windows.WAIT_OBJECT_0 {
							t.Error("owned audio player did not terminate during cleanup")
						}
					}
					windows.CloseHandle(playerHandle)
				}
				if controllerHandle != 0 {
					windows.CloseHandle(controllerHandle)
				}
			})
			require.NoError(t, process.Start())
			started = true
			go func() { waitErr = process.Wait(); close(waitDone) }()
			controllerHandle, err = windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(process.Process.Pid))
			require.NoError(t, err)
			awaitReceipt := func(path string, receipt any) {
				t.Helper()
				for {
					data, err := os.ReadFile(path)
					if err == nil {
						require.NoError(t, json.Unmarshal(data, receipt), "invalid atomic audio receipt")
						return
					}
					// Retry Windows sharing conflicts within the existing receipt deadline.
					if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
						require.NoError(t, err)
					}
					select {
					case <-waitDone:
						t.Fatalf("audio controller exited before receipt: %v; stderr=%q", waitErr, stderr.text())
					case <-ctx.Done():
						t.Fatal("audio controller did not publish a receipt before its deadline")
					case <-time.After(5 * time.Millisecond):
					}
				}
			}
			var player audioWorkbenchHelperReceipt
			awaitReceipt(receiptPath, &player)
			require.Positive(t, player.PID)
			require.Empty(t, player.ForbiddenEnvironment, "playback helper inherited private environment variables")
			require.Equal(t, original, player.Input, "player did not receive verified original samples")
			playerHandle, err = windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(player.PID))
			require.NoError(t, err)
			state, err := windows.WaitForSingleObject(playerHandle, 0)
			require.NoError(t, err)
			require.Equal(t, uint32(windows.WAIT_TIMEOUT), state, "player exited before control delivery")
			snapshots, err := filepath.Glob(filepath.Join(store.Directory, ".snapshot-*"))
			require.NoError(t, err)
			require.Len(t, snapshots, 1, "active playback did not retain its private snapshot")
			state, err = windows.WaitForSingleObject(controllerHandle, 0)
			require.NoError(t, err)
			require.Equal(t, uint32(windows.WAIT_TIMEOUT), state, "controller exited before control delivery")
			written, err := input.Write([]byte{byte(tc.event)})
			require.NoError(t, err)
			require.Equal(t, 1, written)

			var result windowsConsoleResult
			awaitReceipt(resultPath, &result)
			require.True(t, result.Started)
			require.True(t, result.EventSent)
			require.Equal(t, tc.event, result.Event)
			require.NotNil(t, result.ChildExit)
			require.Equal(t, 130, *result.ChildExit)
			require.False(t, result.TimedOut)
			require.False(t, result.ForcedKill)
			require.NoError(t, ctx.Err())
			state, err = windows.WaitForSingleObject(controllerHandle, 0)
			require.NoError(t, err)
			require.Equal(t, uint32(windows.WAIT_TIMEOUT), state, "controller released its Job Object before cleanup checks")
			state, err = windows.WaitForSingleObject(playerHandle, 0)
			require.NoError(t, err)
			require.Equal(t, uint32(windows.WAIT_OBJECT_0), state, "CLI exited without terminating the direct player")
			var playerExit uint32
			require.NoError(t, windows.GetExitCodeProcess(playerHandle, &playerExit))
			require.NotEqual(t, uint32(124), playerExit, "fixture timeout cannot establish CLI cleanup")
			require.NotEqual(t, uint32(windows.STATUS_CONTROL_C_EXIT), playerExit, "console termination cannot establish CLI cleanup")
			requireNoAudioSnapshots(t, store)
			for path, expected := range originalFiles {
				retained, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, expected, retained, "cancellation changed retained original bytes or metadata")
			}
			state, err = windows.WaitForSingleObject(controllerHandle, 0)
			require.NoError(t, err)
			require.Equal(t, uint32(windows.WAIT_TIMEOUT), state, "controller exited during cleanup checks")
			// Only this acknowledgement permits controller exit and Job Object teardown.
			written, err = input.Write([]byte{1})
			require.NoError(t, err)
			require.Equal(t, 1, written)
			require.NoError(t, input.Close())
			select {
			case <-waitDone:
			case <-ctx.Done():
				t.Fatal("audio controller did not exit after acknowledgement")
			}
			require.NoError(t, ctx.Err())
			var exit *exec.ExitError
			require.True(t, errors.As(waitErr, &exit), "controller result=%v", waitErr)
			require.Equal(t, 130, exit.ExitCode())
			require.Empty(t, stdout.checkedText(t))
			diagnostic := stderr.checkedText(t)
			for _, private := range []string{store.Directory, "synthetic-private", "Private synthetic prompt", "Private synthetic instructions"} {
				require.NotContains(t, diagnostic, private)
			}
			if diagnostic != "" {
				var structured map[string]any
				require.NoError(t, json.Unmarshal([]byte(diagnostic), &structured), "structured cancellation error contains prose")
			}
		})
	}
}
