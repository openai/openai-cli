//go:build !windows

package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStreamOutputErrorOrigins(t *testing.T) {
	paths := map[string]func(func(*os.File) error) error{
		"stdout":       streamToStdout,
		"pipe pager":   func(generate func(*os.File) error) error { return streamToPagerWithPipe("test", generate) },
		"socket pager": func(generate func(*os.File) error) error { return streamOutputOSSpecific("test", generate) },
	}
	for name, stream := range paths {
		t.Run(name, func(t *testing.T) {
			t.Setenv("PAGER", "cat")
			output, err := os.CreateTemp(t.TempDir(), "output")
			require.NoError(t, err)
			defer output.Close()
			previous := os.Stdout
			os.Stdout = output
			t.Cleanup(func() { os.Stdout = previous })
			for _, upstream := range []error{errors.New("ordinary failure"), errors.New("upstream broken pipe"), fmt.Errorf("transport: %w", syscall.EPIPE)} {
				require.ErrorIs(t, stream(func(w *os.File) error {
					_, err := w.WriteString("partial output\n")
					require.NoError(t, err)
					return upstream
				}), upstream)
			}
			ready := filepath.Join(t.TempDir(), "ready")
			pager := filepath.Join(t.TempDir(), "pager")
			require.NoError(t, os.WriteFile(pager, []byte("#!/bin/sh\nexec 0<&-\nprintf ready > \"$C02_PAGER_READY\"\n"), 0700))
			t.Setenv("PAGER", pager)
			t.Setenv("C02_PAGER_READY", ready)
			if name == "stdout" {
				r, w, err := os.Pipe()
				require.NoError(t, err)
				require.NoError(t, r.Close())
				defer w.Close()
				os.Stdout = w
			}
			var writeErr error
			err = stream(func(w *os.File) error {
				if name != "stdout" {
					// Wait for the reader to close before writing: closing a socket
					// with unread data can instead produce ECONNRESET.
					require.Eventually(t, func() bool {
						_, err := os.Stat(ready)
						return err == nil
					}, 5*time.Second, time.Millisecond)
				}
				for i := 0; i < 100; i++ {
					if _, writeErr = w.WriteString(strings.Repeat("x", 65536)); writeErr != nil {
						return &outputWriteError{writeErr}
					}
				}
				return nil
			})
			require.ErrorIs(t, writeErr, syscall.EPIPE)
			require.NoError(t, err)
		})
	}
}

func TestShowJSONIteratorClosedOutput(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	require.NoError(t, r.Close())
	defer w.Close()
	previous := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = previous })
	iter := &sliceIterator[string]{items: []string{strings.Repeat("x", 4000)}}
	require.NoError(t, ShowJSONIterator[string](iter, -1, ShowJSONOpts{Format: "raw", Stdout: w}))
}

func TestStreamOutputReapsPagerOnGenerationError(t *testing.T) {
	paths := map[string]func(string, func(*os.File) error) error{
		"pipe":   streamToPagerWithPipe,
		"socket": streamOutputOSSpecific,
	}
	for name, stream := range paths {
		for _, exitCode := range []string{"0", "7"} {
			for _, upstream := range []error{nil, errors.New("upstream broken pipe"), fmt.Errorf("transport: %w", syscall.EPIPE), errors.New("formatting failed")} {
				t.Run(fmt.Sprintf("%s/exit%s/%v", name, exitCode, upstream), func(t *testing.T) {
					dir := t.TempDir()
					pidPath, outputPath := filepath.Join(dir, "pid"), filepath.Join(dir, "output")
					pager := filepath.Join(dir, "pager")
					require.NoError(t, os.WriteFile(pager, []byte("#!/bin/sh\nprintf '%s' \"$$\" > \"$C02_PAGER_PID\"\ncat > \"$C02_PAGER_OUTPUT\"\nexit \"$C02_PAGER_EXIT\"\n"), 0700))
					t.Setenv("PAGER", pager)
					t.Setenv("C02_PAGER_PID", pidPath)
					t.Setenv("C02_PAGER_OUTPUT", outputPath)
					t.Setenv("C02_PAGER_EXIT", exitCode)
					var writer *os.File
					pid := 0
					err := stream("test", func(w *os.File) error {
						writer = w
						require.Eventually(t, func() bool {
							data, err := os.ReadFile(pidPath)
							if err != nil {
								return false
							}
							pid, err = strconv.Atoi(string(data))
							return err == nil && pid > 0
						}, 5*time.Second, time.Millisecond)
						// Reap even when testing an implementation that returns too early.
						t.Cleanup(func() { var status syscall.WaitStatus; _, _ = syscall.Wait4(pid, &status, 0, nil) })
						if name == "socket" {
							require.Equal(t, "parent-socket", w.Name())
						}
						_, err := w.WriteString("partial output\n")
						require.NoError(t, err)
						return upstream
					})
					if upstream != nil {
						require.Same(t, upstream, err)
					} else if exitCode == "0" {
						require.NoError(t, err)
					} else {
						require.Error(t, err)
					}
					require.NotNil(t, writer)
					_, statErr := writer.Stat()
					require.ErrorIs(t, statErr, os.ErrClosed)
					var status syscall.WaitStatus
					_, waitErr := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
					require.ErrorIs(t, waitErr, syscall.ECHILD, "pager must already be reaped before return")
					output, err := os.ReadFile(outputPath)
					require.NoError(t, err)
					require.Equal(t, "partial output\n", string(output))
				})
			}
		}
	}
}
