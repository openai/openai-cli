//go:build !windows

package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
