//go:build !windows

package custom

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestSocketPairPagerFailureKeepsReusedDescriptorOpen(t *testing.T) {
	const childEnv = "OPENAI_CLI_PAGER_DESCRIPTOR_TEST"
	failure := os.Getenv(childEnv)
	if failure == "" {
		for _, failure := range []string{"LookPath", "ForkExec"} {
			t.Run(failure, func(t *testing.T) {
				// Isolate descriptor allocation, GC and finalizers from other tests.
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSocketPairPagerFailureKeepsReusedDescriptorOpen$")
				child.Env = append(os.Environ(), childEnv+"="+failure)
				output, err := child.CombinedOutput()
				require.NoError(t, err, "%s", output)
			})
		}
		return
	}
	// Keep an abandoned os.File alive until the descriptor has been reused.
	previousGC := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(previousGC)
	// Initialize the runtime poller before reserving descriptor numbers.
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.NoError(t, writer.Close())
	pager := filepath.Join(t.TempDir(), "missing-pager")
	if failure == "ForkExec" {
		pager = filepath.Join(t.TempDir(), "invalid-executable")
		require.NoError(t, os.WriteFile(pager, []byte("not an executable format\n"), 0700))
	}
	t.Setenv("PAGER", pager)

	// Unix reuses the lowest free descriptor. Probe it without creating
	// os.File wrappers or finalizers of our own.
	probe, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	require.NoError(t, err)
	require.NoError(t, unix.Close(probe[0]))
	require.NoError(t, unix.Close(probe[1]))
	file, pid, err := openSocketPairPager("synthetic descriptor regression")
	require.Error(t, err)
	require.Nil(t, file)
	require.Zero(t, pid)

	reused, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	require.NoError(t, err)
	defer unix.Close(reused[0])
	defer unix.Close(reused[1])
	require.Equal(t, probe, reused, "the replacement must reuse the pager's descriptors")
	for range 20 {
		runtime.GC()
		// Finalizers run asynchronously after GC. Give them a turn before
		// checking that they cannot close the replacement socket.
		time.Sleep(time.Millisecond)
		_, err := unix.FcntlInt(uintptr(reused[0]), unix.F_GETFD, 0)
		require.NoError(t, err, "pager finalizer closed the reused descriptor")
	}
}
