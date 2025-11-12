package binaryparam

import (
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileOrStdin(t *testing.T) {
	t.Parallel()

	const expectedContents = "test file contents"

	t.Run("WithFile", func(t *testing.T) {
		tempFile := t.TempDir() + "/test_file.txt"
		require.NoError(t, os.WriteFile(tempFile, []byte(expectedContents), 0600))

		readCloser, stdinInUse, err := FileOrStdin(os.Stdin, tempFile)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, readCloser.Close()) })

		actualContents, err := io.ReadAll(readCloser)
		require.NoError(t, err)
		require.Equal(t, expectedContents, string(actualContents))

		require.False(t, stdinInUse)
	})

	stdinTests := []struct {
		testName string
		path     string
	}{
		{"TestEmptyString", ""},
		{"TestDash", "-"},
		{"TestDevStdin", "/dev/stdin"},
		{"TestDevFD0", "/dev/fd/0"},
	}
	for _, test := range stdinTests {
		t.Run(test.testName, func(t *testing.T) {
			tempFile := t.TempDir() + "/test_file.txt"
			require.NoError(t, os.WriteFile(tempFile, []byte(expectedContents), 0600))

			stubStdin, err := os.Open(tempFile)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, stubStdin.Close()) })

			readCloser, stdinInUse, err := FileOrStdin(stubStdin, test.path)
			require.NoError(t, err)

			actualContents, err := io.ReadAll(readCloser)
			require.NoError(t, err)
			require.Equal(t, expectedContents, string(actualContents))

			require.True(t, stdinInUse)
		})
	}
}
