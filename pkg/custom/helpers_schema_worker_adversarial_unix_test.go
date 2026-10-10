//go:build unix

package custom

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These workers exercise the parent protocol without invoking a model or an
// expensive compiler input. Every helper uses shell builtins and exits locally.
const adversarialSchemaWorker = `#!/bin/sh
[ "$#" -eq 1 ] && [ "$1" = '__openai-schema-validate' ] || exit 9
[ -z "${OPENAI_API_KEY+x}" ] && [ -z "${OPENAI_CUSTOM_HEADERS+x}" ] || exit 8
IFS= read -r mode
case "$mode" in
  valid) printf 'openai-schema-valid-v1\n'; exit 0 ;;
  invalid) printf 'openai-schema-invalid-v1\n'; exit 2 ;;
  empty) exit 0 ;;
  truncated) printf 'openai-schema-valid-v1'; exit 0 ;;
  invalid-success) printf 'openai-schema-invalid-v1\n'; exit 0 ;;
  valid-failure) printf 'openai-schema-valid-v1\n'; exit 2 ;;
  crashed) printf 'openai-schema-invalid-v1\n'; exit 3 ;;
  extra) printf 'openai-schema-valid-v1\nextra\n'; exit 0 ;;
  oversized) printf '%065d' 0; exit 0 ;;
  stderr) printf 'synthetic private diagnostic\n' >&2; printf 'openai-schema-valid-v1\n'; exit 0 ;;
  started)
    IFS= read -r ready
    printf '%s\n' "$$" > "$ready" || exit 7
    while :; do :; done
    ;;
esac
exit 6
`

func schemaAdversarialExecutable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "schema compiler fixture")
	require.NoError(t, os.WriteFile(path, []byte(adversarialSchemaWorker), 0700))
	return path
}

func TestHelperSchemaCompilerAdversarialAcknowledgement(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-fake-worker-parent")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "X-Fixture: fake-private-parent")
	executable := schemaAdversarialExecutable(t)
	for _, tc := range []struct {
		mode string
		kind string
	}{
		{"valid", "valid"},
		{"invalid", "invalid"},
		{"empty", "local"},
		{"truncated", "local"},
		{"invalid-success", "local"},
		{"valid-failure", "local"},
		{"crashed", "local"},
		{"extra", "local"},
		{"oversized", "local"},
		{"stderr", "valid"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			err := runSchemaCompiler(ctx, executable, []byte(tc.mode+"\n"))
			switch tc.kind {
			case "valid":
				require.NoError(t, err)
			case "invalid":
				require.ErrorIs(t, err, errHelperSchemaInvalid)
			default:
				require.Error(t, err)
				require.NotErrorIs(t, err, errHelperSchemaInvalid)
				var local *schemaHelperError
				require.ErrorAs(t, err, &local)
				require.Contains(t, local.Error(), "Local schema compiler")
				require.NotContains(t, local.Error(), "synthetic private diagnostic")
			}
		})
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := runSchemaCompiler(ctx, filepath.Join(t.TempDir(), "missing-worker"), []byte("valid\n"))
	require.Error(t, err)
	require.NotErrorIs(t, err, errHelperSchemaInvalid)
	var local *schemaHelperError
	require.ErrorAs(t, err, &local)
}

func TestHelperSchemaCompilerAdversarialStartedCancellationReapsChild(t *testing.T) {
	executable := schemaAdversarialExecutable(t)
	ready := filepath.Join(t.TempDir(), "worker-ready")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	done := make(chan error, 1)
	completed := false
	defer func() {
		cancel()
		if !completed {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("worker cleanup did not finish")
			}
		}
	}()
	go func() {
		done <- runSchemaCompiler(ctx, executable, []byte("started\n"+ready+"\n"))
	}()
	// The child publishes its PID before entering its blocking work phase.
	// Wait for that handshake, rather than canceling after an arbitrary sleep.
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	pid := 0
	for pid == 0 {
		data, err := os.ReadFile(ready)
		if err == nil && strings.HasSuffix(string(data), "\n") {
			pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
			require.NoError(t, err)
			break
		}
		select {
		case err := <-done:
			completed = true
			t.Fatalf("worker stopped before its ready handshake: %v", err)
		case <-ctx.Done():
			t.Fatal("worker did not reach its ready handshake")
		case <-ticker.C:
		}
	}
	started := time.Now()
	cancel()
	var err error
	select {
	case err = <-done:
		completed = true
	case <-time.After(3 * time.Second):
		t.Fatal("started worker did not stop after cancellation")
	}
	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, errHelperSchemaInvalid)
	var exit *exec.ExitError
	require.True(t, errors.As(err, &exit), "missing child exit result: %v", err)
	require.Equal(t, pid, exit.ProcessState.Pid())
	require.Less(t, time.Since(started), 3*time.Second)
	var status syscall.WaitStatus
	_, waitErr := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	require.ErrorIs(t, waitErr, syscall.ECHILD, "worker must already be reaped")
}
