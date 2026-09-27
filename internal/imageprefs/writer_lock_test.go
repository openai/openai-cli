package imageprefs

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Done is first observed by the contended lock's cancellable wait. The signal
// lets tests advance the current writer without depending on sleep durations.
type observedWriterWait struct {
	context.Context
	once    sync.Once
	waiting func()
}

func (ctx *observedWriterWait) Done() <-chan struct{} {
	ctx.once.Do(ctx.waiting)
	return ctx.Context.Done()
}

func TestImagePreferenceWaitingWriterProcess(t *testing.T) {
	path := os.Getenv("OPENAI_IMAGE_PREFS_WAITING_WRITER")
	if path == "" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	observed := &observedWriterWait{Context: ctx, waiting: func() { fmt.Println("waiting") }}
	require.ErrorContains(t, Save(observed, path, true), "unknown or invalid image preferences")
}

func TestImagePreferenceWaitingWriterKeepsNewSchema(t *testing.T) {
	for _, initial := range []string{"missing", "valid"} {
		for _, replacement := range []string{`{"version":2,"inline":false}`, `{"version":1,"inline":false,"future":"keep"}`} {
			t.Run(initial+"/"+replacement, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "image-preferences.json")
				if initial == "valid" {
					require.NoError(t, Save(t.Context(), path, false))
				}
				root, name, err := openParent(path, true)
				require.NoError(t, err)
				defer root.Close()
				firstWriter, err := lockPreferences(t.Context(), root, name)
				require.NoError(t, err)
				defer firstWriter.Close()
				binary, err := os.Executable()
				require.NoError(t, err)
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				child := exec.CommandContext(ctx, binary, "-test.run=^TestImagePreferenceWaitingWriterProcess$")
				for _, entry := range os.Environ() {
					name, _, _ := strings.Cut(entry, "=")
					if !strings.HasPrefix(strings.ToUpper(name), "OPENAI_") {
						child.Env = append(child.Env, entry)
					}
				}
				child.Env = append(child.Env, "OPENAI_IMAGE_PREFS_WAITING_WRITER="+path)
				output, err := child.StdoutPipe()
				require.NoError(t, err)
				var diagnostics bytes.Buffer
				child.Stderr = &diagnostics
				require.NoError(t, child.Start())
				t.Cleanup(func() {
					if child.ProcessState == nil {
						_ = child.Process.Kill()
						_ = child.Wait()
					}
				})
				reader := bufio.NewReader(output)
				ready, err := reader.ReadString('\n')
				require.NoError(t, err)
				require.Equal(t, "waiting\n", ready)
				// Simulate a newer cooperating writer publishing while holding
				// the lock. The waiting Save must validate this new snapshot.
				temporary := filepath.Join(filepath.Dir(path), "upgraded.json")
				require.NoError(t, os.WriteFile(temporary, []byte(replacement), 0600))
				require.NoError(t, os.Rename(temporary, path))
				require.NoError(t, firstWriter.Close())
				require.NoError(t, child.Wait(), diagnostics.String())
				require.NoError(t, ctx.Err())
				current, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, replacement, string(current))
			})
		}
	}
}

func TestImagePreferenceWaitingWriterCancellationAndReplacement(t *testing.T) {
	for _, change := range []string{"cancel", "replace-lock"} {
		t.Run(change, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "image-preferences.json")
			require.NoError(t, Save(t.Context(), path, false))
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			root, name, err := openParent(path, false)
			require.NoError(t, err)
			defer root.Close()
			firstWriter, err := lockPreferences(t.Context(), root, name)
			require.NoError(t, err)
			defer firstWriter.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			waiting := make(chan struct{})
			observed := &observedWriterWait{Context: ctx, waiting: func() { close(waiting) }}
			result := make(chan error, 1)
			go func() { result <- Save(observed, path, true) }()
			select {
			case <-waiting:
			case err := <-result:
				t.Fatalf("writer did not wait: %v", err)
			case <-ctx.Done():
				t.Fatal("writer did not reach lock wait")
			}
			if change == "cancel" {
				cancel()
				select {
				case err := <-result:
					require.ErrorIs(t, err, context.Canceled)
				case <-time.After(time.Second):
					t.Fatal("waiting writer ignored cancellation")
				}
			} else {
				lockPath := filepath.Join(filepath.Dir(path), "."+name+".lock")
				require.NoError(t, os.Rename(lockPath, lockPath+".old"))
				require.NoError(t, os.WriteFile(lockPath, nil, 0600))
				require.NoError(t, firstWriter.Close())
				require.ErrorContains(t, <-result, "lock changed")
			}
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func TestImagePreferenceRejectsUnsafeWriterLocks(t *testing.T) {
	for _, kind := range []string{"directory", "symlink", "dangling-symlink", "public"} {
		t.Run(kind, func(t *testing.T) {
			if runtime.GOOS == "windows" && kind != "directory" {
				t.Skip("native Windows symlink privileges and ACLs differ")
			}
			path := filepath.Join(t.TempDir(), "image-preferences.json")
			lock := filepath.Join(filepath.Dir(path), ".image-preferences.json.lock")
			target := filepath.Join(t.TempDir(), "target")
			switch kind {
			case "directory":
				require.NoError(t, os.Mkdir(lock, 0700))
			case "symlink":
				require.NoError(t, os.WriteFile(target, []byte("kept"), 0600))
				require.NoError(t, os.Symlink(target, lock))
			case "dangling-symlink":
				require.NoError(t, os.Symlink(target, lock))
			case "public":
				require.NoError(t, os.WriteFile(lock, nil, 0600))
				require.NoError(t, os.Chmod(lock, 0666))
			}
			require.Error(t, Save(t.Context(), path, true))
			_, err := os.Stat(path)
			require.ErrorIs(t, err, os.ErrNotExist)
			if kind == "symlink" {
				data, err := os.ReadFile(target)
				require.NoError(t, err)
				require.Equal(t, "kept", string(data))
			} else if kind == "dangling-symlink" {
				_, err := os.Stat(target)
				require.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
}
