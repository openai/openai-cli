//go:build darwin || linux

package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMainRequestFileAliasCannotConsumeFieldStdin(t *testing.T) {
	link := filepath.Join(t.TempDir(), "stdin-link")
	require.NoError(t, os.Symlink("/dev/stdin", link))
	for _, path := range []string{"/dev/../dev/stdin", link} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			server, requests := globalFlagsServer(t, `{}`)
			input, writer, err := os.Pipe()
			require.NoError(t, err)
			t.Cleanup(func() { input.Close() })
			const source = `{"model":"synthetic"}`
			_, err = io.WriteString(writer, source)
			require.NoError(t, writer.Close())
			require.NoError(t, err)

			got := runShellFileCommand(t, server, input, nil, "responses", "create",
				"--request-file", path, "--input", "@-")
			require.NotZero(t, got.code)
			require.Empty(t, got.stdout)
			require.JSONEq(t, `{"message":"use a regular --request-file when a request parameter reads stdin"}`, got.stderr)
			require.Empty(t, requests)
			left, err := io.ReadAll(input)
			require.NoError(t, err)
			require.Equal(t, source, string(left))
		})
	}
}
