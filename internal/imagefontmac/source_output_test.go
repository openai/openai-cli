package imagefontmac

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSourceOutputHelperProcess(t *testing.T) {
	mode := os.Getenv("IMAGEFONT_SOURCE_HELPER")
	if mode == "" {
		return
	}
	if err := os.WriteFile(os.Getenv("IMAGEFONT_SOURCE_READY"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		os.Exit(2)
	}
	count, _ := strconv.Atoi(os.Getenv("IMAGEFONT_SOURCE_BYTES"))
	if mode == "descendant" {
		ready := os.Getenv("IMAGEFONT_SOURCE_READY") + ".descendant"
		child := exec.Command(os.Args[0], "-test.run=^TestSourceOutputHelperProcess$")
		child.Env = []string{"IMAGEFONT_SOURCE_HELPER=wait", "IMAGEFONT_SOURCE_WAIT=1", "IMAGEFONT_SOURCE_READY=" + ready}
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if child.Start() != nil {
			os.Exit(2)
		}
		for i := 0; i < 500; i++ {
			if _, err := os.Stat(ready); err == nil {
				os.Exit(0)
			}
			time.Sleep(10 * time.Millisecond)
		}
		_ = child.Process.Kill()
		_ = child.Wait()
		os.Exit(2)
	}
	if mode == "stdout" {
		_, _ = os.Stdout.Write(bytes.Repeat([]byte{'x'}, count))
	} else if mode == "stderr" {
		_, _ = os.Stderr.Write(bytes.Repeat([]byte{'x'}, count))
	}
	if os.Getenv("IMAGEFONT_SOURCE_WAIT") == "1" {
		time.Sleep(15 * time.Second)
	}
	os.Exit(0)
}

func sourceHelper(t *testing.T, mode string, count int, wait bool) (string, []string, []string, string) {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	ready := filepath.Join(t.TempDir(), "ready")
	env := []string{"IMAGEFONT_SOURCE_HELPER=" + mode, "IMAGEFONT_SOURCE_BYTES=" + strconv.Itoa(count), "IMAGEFONT_SOURCE_READY=" + ready}
	if wait {
		env = append(env, "IMAGEFONT_SOURCE_WAIT=1")
	}
	return executable, []string{"-test.run=^TestSourceOutputHelperProcess$"}, env, ready
}

func requireSourceHelperReaped(t *testing.T, ready string) {
	t.Helper()
	data, err := os.ReadFile(ready)
	require.NoError(t, err)
	pid, err := strconv.Atoi(string(data))
	require.NoError(t, err)
	process, err := os.FindProcess(pid)
	if err != nil {
		return // Windows can report that the process is already gone.
	}
	_, err = process.Wait()
	require.Error(t, err, "the runner must have already waited for the child")
}

func TestSourceOutputBoundsSubprocessStreams(t *testing.T) {
	for _, test := range []struct {
		name, stream string
		bytes        int
		exceeded     bool
	}{
		{"stdout exact", "stdout", 1024, false},
		{"stdout overflow", "stdout", 1025, true},
		{"stderr exact", "stderr", 256, false},
		{"stderr overflow", "stderr", 257, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			program, args, env, ready := sourceHelper(t, test.stream, test.bytes, test.exceeded)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			data, err := runSourceOutput(ctx, program, args, env, 1024, 256)
			if test.exceeded {
				require.ErrorIs(t, err, errSourceOutputLimit)
				require.Empty(t, data)
			} else {
				require.NoError(t, err)
				if test.stream == "stdout" {
					require.Equal(t, bytes.Repeat([]byte{'x'}, test.bytes), data)
				} else {
					require.Empty(t, data, "stderr must never be returned as font data")
				}
			}
			requireSourceHelperReaped(t, ready)
		})
	}
}

func TestSourceOutputCancellationReapsChild(t *testing.T) {
	program, args, env, ready := sourceHelper(t, "wait", 0, true)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := runSourceOutput(ctx, program, args, env, 1024, 256)
		done <- err
	}()
	require.Eventually(t, func() bool {
		_, err := os.Stat(ready)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("source bridge did not stop after cancellation")
	}
	requireSourceHelperReaped(t, ready)
}

func TestSourceOutputClosesDescendantPipesAfterExit(t *testing.T) {
	program, args, env, ready := sourceHelper(t, "descendant", 0, false)
	// The grandchild deliberately keeps the parent's pipes open. Clean up this
	// synthetic process even if the output runner fails its wait-delay contract.
	t.Cleanup(func() {
		data, err := os.ReadFile(ready + ".descendant")
		if err != nil {
			return
		}
		pid, err := strconv.Atoi(string(data))
		if err == nil {
			if process, err := os.FindProcess(pid); err == nil {
				_ = process.Kill()
			}
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	data, err := runSourceOutput(ctx, program, args, env, 1024, 256)
	require.ErrorIs(t, err, exec.ErrWaitDelay)
	require.NoError(t, ctx.Err(), "inherited pipes must close before the caller's deadline")
	require.Empty(t, data)
	requireSourceHelperReaped(t, ready)
}

func TestSourceOutputCancelledBeforeLaunch(t *testing.T) {
	program, args, env, ready := sourceHelper(t, "wait", 0, true)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := runSourceOutput(ctx, program, args, env, 1024, 256)
	require.ErrorIs(t, err, context.Canceled)
	require.NoFileExists(t, ready)
}

func TestSourcePreservesUsefulExportLimitErrors(t *testing.T) {
	for _, native := range []bool{false, true} {
		_, err := source(t.Context(), "Synthetic", 13, func() bool { return true }, func(context.Context, string, []string, []string) ([]byte, error) {
			if native {
				return []byte(`{"ok":false,"reason":"size"}`), nil
			}
			return nil, errors.Join(errSourceOutputLimit, errors.New("private diagnostics"))
		})
		require.ErrorContains(t, err, "limit")
		require.NotContains(t, err.Error(), "private")
	}
}

func TestSourceValidatesDecodedFaceBudget(t *testing.T) {
	data := make([]byte, maxSourceFaceBytes+1)
	font := sourceFixture("Synthetic")
	font.Tables = map[string][]byte{"glyf": data[:maxSourceFaceBytes]}
	require.True(t, validSource(font), "the existing 64 MiB per-face limit remains supported")
	font.Tables["head"] = data[:1]
	require.False(t, validSource(font), "table sizes must be added across each face")
	font.Tables = map[string][]byte{"glyf": data}
	require.False(t, validSource(font))
}
