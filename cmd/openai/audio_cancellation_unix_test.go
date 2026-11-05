//go:build !windows

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/stretchr/testify/require"
)

func startAudioLocalProcess(t *testing.T, env []string, stdout io.Writer, args ...string) (*exec.Cmd, *bytes.Buffer, <-chan error, context.Context) {
	t.Helper()
	binary, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	command := exec.CommandContext(ctx, binary, append([]string{"-test.run=^TestMainDispatchProcess$", "--", "openai"}, args...)...)
	command.Env = append(append([]string(nil), env...), "OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "GOMAXPROCS=2")
	stderr := new(bytes.Buffer)
	command.Stdout, command.Stderr = stdout, stderr
	require.NoError(t, command.Start())
	done := make(chan error, 1)
	go func() { done <- command.Wait(); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return command, stderr, done, ctx
}

func requireAudioSignalExit(t *testing.T, done <-chan error, ctx context.Context, stdout, stderr *bytes.Buffer, code int, private string) {
	t.Helper()
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		t.Fatal("local audio cancellation did not finish promptly")
	}
	require.NoError(t, ctx.Err())
	if err == nil {
		t.Logf("unexpected successful cancellation: stdout_bytes=%d stderr=%q", stdout.Len(), stderr.String())
	}
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	require.Equal(t, code, exit.ExitCode(), "stderr=%q", stderr.String())
	require.Empty(t, stdout.String())
	require.NotContains(t, stderr.String(), private)
	require.NotContains(t, stderr.String(), "Private synthetic prompt")
	if stderr.Len() != 0 {
		var diagnostic map[string]any
		require.NoError(t, json.Unmarshal(stderr.Bytes(), &diagnostic), "structured cancellation error contains prose")
	}
}

func TestMainAudioWorkbenchPlayerCancellationStatusAndCleanup(t *testing.T) {
	for _, tc := range []struct {
		name   string
		signal syscall.Signal
		code   int
	}{{"SIGINT", syscall.SIGINT, 130}, {"SIGTERM", syscall.SIGTERM, 143}} {
		t.Run(tc.name, func(t *testing.T) {
			env, store, entry, _, original := audioWorkbenchHistory(t)
			programs := t.TempDir()
			ready := filepath.Join(t.TempDir(), "player-ready")
			quote := "'" + strings.ReplaceAll(ready, "'", "'\\''") + "'"
			script := "#!/bin/sh\nprintf '%s\\n' \"$$\" > " + quote + "\n/bin/cat > /dev/null\nexec /bin/sleep 60\n"
			require.NoError(t, os.WriteFile(filepath.Join(programs, "ffplay"), []byte(script), 0700))
			env = append(env, "PATH="+programs, "OPENAI_BASE_URL=://synthetic-private-endpoint")
			var stdout bytes.Buffer
			child, stderr, done, ctx := startAudioLocalProcess(t, env, &stdout, "--format-error", "json", "audio", "play", entry.ID)
			helperPID := 0
			t.Cleanup(func() {
				if helperPID == 0 {
					if data, err := os.ReadFile(ready); err == nil {
						helperPID, _ = strconv.Atoi(strings.TrimSpace(string(data)))
					}
				}
				if helperPID != 0 {
					_ = syscall.Kill(-helperPID, syscall.SIGKILL)
				}
			})
			for helperPID == 0 {
				if data, err := os.ReadFile(ready); err == nil && strings.TrimSpace(string(data)) != "" {
					helperPID, err = strconv.Atoi(strings.TrimSpace(string(data)))
					require.NoError(t, err)
					break
				}
				select {
				case <-done:
					t.Fatalf("play exited before its fake player became ready: %s", stderr.String())
				case <-ctx.Done():
					t.Fatal("fake player did not become ready")
				case <-time.After(time.Millisecond):
				}
			}
			require.NoError(t, child.Process.Signal(tc.signal))
			requireAudioSignalExit(t, done, ctx, &stdout, stderr, tc.code, store.Directory)
			require.ErrorIs(t, syscall.Kill(helperPID, 0), syscall.ESRCH, "player was not reaped before CLI exit")
			require.NoError(t, os.Remove(ready))
			helperPID = 0
			requireNoAudioSnapshots(t, store)
			retained, err := os.ReadFile(filepath.Join(store.Directory, entry.ID, "audio"))
			require.NoError(t, err)
			require.Equal(t, original, retained)
		})
	}
}

type audioCancellationZeros struct{}

func (audioCancellationZeros) Read(buffer []byte) (int, error) {
	clear(buffer)
	return len(buffer), nil
}

func TestMainAudioWorkbenchSnapshotCancellationStatusAndCleanup(t *testing.T) {
	env, store, _, _, _ := audioWorkbenchHistory(t)
	// A large sparse fixture exposes the real snapshot-copy phase. Its size is
	// a cancellation probe, not a new maximum for retained audio.
	const size int64 = 256 << 20
	const id = "cccccccccccccccccccccccccccccccc"
	directory := filepath.Join(store.Directory, id)
	require.NoError(t, os.Mkdir(directory, 0700))
	file, err := os.OpenFile(filepath.Join(directory, "audio"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	require.NoError(t, err)
	require.NoError(t, file.Truncate(size))
	require.NoError(t, file.Close())
	digest := sha256.New()
	_, err = io.Copy(digest, io.LimitReader(audioCancellationZeros{}, size))
	require.NoError(t, err)
	entry := audiohistory.Entry{ID: id, CreatedAt: time.Now().UTC(), Bytes: size, SHA256: hex.EncodeToString(digest.Sum(nil)),
		Settings: audiohistory.Settings{Input: "Synthetic large audio", Model: "synthetic-model", Voice: "coral", Format: "pcm", Speed: 1}}
	metadata, err := json.Marshal(map[string]any{"version": 1, "entry": entry})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(directory, "manifest.json"), metadata, 0600))
	for _, operation := range []string{"inspect", "export"} {
		for _, tc := range []struct {
			name   string
			signal syscall.Signal
			code   int
		}{
			{"SIGINT", syscall.SIGINT, 130}, {"SIGTERM", syscall.SIGTERM, 143},
		} {
			t.Run(operation+"/"+tc.name, func(t *testing.T) {
				var stdout bytes.Buffer
				args := []string{"--format", "json", "--format-error", "json", "audio", operation, id}
				destination := filepath.Join(t.TempDir(), "existing.pcm")
				require.NoError(t, os.WriteFile(destination, []byte("previous exported audio"), 0600))
				if operation == "export" {
					args = append(args, "--output", destination)
				}
				child, stderr, done, ctx := startAudioLocalProcess(t, env, &stdout, args...)
				var snapshot string
				for snapshot == "" {
					entries, err := os.ReadDir(store.Directory)
					require.NoError(t, err)
					for _, candidate := range entries {
						if strings.HasPrefix(candidate.Name(), ".snapshot-") {
							snapshot = filepath.Join(store.Directory, candidate.Name())
							break
						}
					}
					if snapshot != "" {
						break
					}
					select {
					case <-done:
						t.Fatalf("command exited before snapshot copy: %s", stderr.String())
					case <-ctx.Done():
						t.Fatal("snapshot copy never started")
					case <-time.After(time.Millisecond):
					}
				}
				// Pin a read-only descriptor to the growing snapshot. It remains
				// inspectable after cancellation unlinks the owned temporary path.
				snapshotFile, err := os.OpenFile(snapshot, os.O_RDONLY|syscall.O_NONBLOCK, 0)
				require.NoError(t, err, "snapshot completed before the cancellation handshake")
				defer snapshotFile.Close()
				info, err := snapshotFile.Stat()
				require.NoError(t, err)
				require.True(t, info.Mode().IsRegular())
				previous := info.Size()
				for info.Size() <= previous {
					select {
					case <-done:
						t.Fatal("snapshot finished before observed growth")
					case <-ctx.Done():
						t.Fatal("snapshot did not grow")
					case <-time.After(time.Millisecond):
					}
					info, err = snapshotFile.Stat()
					require.NoError(t, err)
				}
				observed := info.Size()
				signalErr := child.Process.Signal(tc.signal)
				afterSignal, statErr := snapshotFile.Stat()
				require.NoError(t, signalErr)
				require.NoError(t, statErr)
				t.Logf("direct signal=%s observed_partial_bytes=%d post_signal_partial_bytes=%d total_bytes=%d observed_fraction=%.6f post_signal_fraction=%.6f",
					tc.name, observed, afterSignal.Size(), size, float64(observed)/float64(size), float64(afterSignal.Size())/float64(size))
				require.Less(t, observed, size, "signal did not target the copy phase")
				require.Less(t, afterSignal.Size(), size, "signal missed the partial-copy phase")
				requireAudioSignalExit(t, done, ctx, &stdout, stderr, tc.code, store.Directory)
				completed, err := snapshotFile.Stat()
				require.NoError(t, err)
				require.Less(t, completed.Size(), size, "copy completed after direct cancellation")
				requireNoAudioSnapshots(t, store)
				preserved, err := os.ReadFile(destination)
				require.NoError(t, err)
				require.Equal(t, "previous exported audio", string(preserved))
				info, err = os.Stat(filepath.Join(directory, "audio"))
				require.NoError(t, err)
				require.Equal(t, size, info.Size())
				source, err := os.Open(filepath.Join(directory, "audio"))
				require.NoError(t, err)
				actual := sha256.New()
				_, hashErr := io.Copy(actual, source)
				require.NoError(t, source.Close())
				require.NoError(t, hashErr)
				require.Equal(t, entry.SHA256, hex.EncodeToString(actual.Sum(nil)), "cancellation changed retained source bytes")
			})
		}
	}
}

func TestMainAudioWorkbenchInspectClosedStdoutExitsQuietly(t *testing.T) {
	env, _, entry, _, _ := audioWorkbenchHistory(t)
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	defer writer.Close()
	_, stderr, done, ctx := startAudioLocalProcess(t, env, writer, "audio", "inspect", entry.ID)
	select {
	case err := <-done:
		require.NoError(t, err, "closed stdout became a CLI failure")
	case <-ctx.Done():
		t.Fatal("inspection did not finish with closed stdout")
	}
	require.Empty(t, stderr.String())
}
