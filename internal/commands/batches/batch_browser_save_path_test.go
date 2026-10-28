package batches

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBatchBrowserDefaultSaveNamesIdentifyBatchAndFile(t *testing.T) {
	require.Equal(t, "batch_example-output.jsonl", batchBrowserDefaultSaveName("batch_example", "output"))
	require.Equal(t, "batch_example-input.jsonl", batchBrowserDefaultSaveName("batch_example", "input"))
	require.Equal(t, "batch_example-error.jsonl", batchBrowserDefaultSaveName("batch_example", "error"))
	require.NotEqual(t, batchBrowserDefaultSaveName("batch_first", "output"), batchBrowserDefaultSaveName("batch_second", "output"))
	for _, id := range []string{"", "../outside", "nested/batch", "nested\\batch", "batch\x1b[31m", strings.Repeat("x", 300)} {
		name := batchBrowserDefaultSaveName(id, "../../outside")
		require.Equal(t, name, filepath.Base(name))
		require.NotContains(t, name, "..")
		require.NotContains(t, name, "\x1b")
		require.Less(t, len(name), 255)
		require.True(t, strings.HasSuffix(name, "-file.jsonl"))
	}
	require.NotEqual(t, batchBrowserDefaultSaveName("a/b", "input"), batchBrowserDefaultSaveName("a\\b", "input"))
}

func TestBatchBrowserSaveDestinationShowsAbsoluteLocationWithoutCreatingIt(t *testing.T) {
	directory := t.TempDir()
	t.Chdir(directory)
	working, err := os.Getwd()
	require.NoError(t, err)
	home := filepath.Join(directory, "home")
	homeVariable := "HOME"
	if runtime.GOOS == "windows" {
		homeVariable = "USERPROFILE"
	}
	t.Setenv(homeVariable, home)
	for _, test := range []struct{ input, expected string }{
		{"batch_demo-input.jsonl", working + string(filepath.Separator) + "batch_demo-input.jsonl"},
		{"file with spaces.jsonl", working + string(filepath.Separator) + "file with spaces.jsonl"},
		{"résultats 日本語.jsonl", working + string(filepath.Separator) + "résultats 日本語.jsonl"},
		{"~/Downloads/results.jsonl", home + string(filepath.Separator) + filepath.FromSlash("Downloads/results.jsonl")},
		{filepath.Join(directory, "explicit.jsonl"), filepath.Join(directory, "explicit.jsonl")},
		{"~someone/results.jsonl", working + string(filepath.Separator) + "~someone/results.jsonl"},
		{"$HOME/results.jsonl", working + string(filepath.Separator) + "$HOME/results.jsonl"},
	} {
		t.Run(test.input, func(t *testing.T) {
			actual, err := batchBrowserSaveDestination(test.input)
			require.NoError(t, err)
			require.Equal(t, test.expected, actual)
		})
	}
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestBatchBrowserSaveDestinationRejectsMissingFilename(t *testing.T) {
	for _, input := range []string{"", "  ", "-", "~", ".", "..", "~/", "folder/", "folder/..", "folder/.", "bad\npath", "bad\rpath", "bad\x00path"} {
		_, err := batchBrowserSaveDestination(input)
		require.Error(t, err, input)
	}
	homeVariable := "HOME"
	if runtime.GOOS == "windows" {
		homeVariable = "USERPROFILE"
	}
	t.Setenv(homeVariable, "relative")
	_, err := batchBrowserSaveDestination("~/result.jsonl")
	require.ErrorContains(t, err, "home folder")
}

func TestBatchBrowserSaveDestinationPreservesSymlinkResolutionAndNoOverwrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix symlink/.. semantics; Windows links require separate native coverage")
	}
	directory := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(directory, "target", "child"), 0700))
	require.NoError(t, os.Symlink(filepath.Join(directory, "target", "child"), filepath.Join(directory, "link")))
	t.Chdir(directory)
	path, err := batchBrowserSaveDestination("link/../output.jsonl")
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(path, "/link/../output.jsonl"))
	_, err = writeBatchDownload(t.Context(), path, io.NopCloser(strings.NewReader("first")))
	require.NoError(t, err)
	actual := filepath.Join(directory, "target", "output.jsonl")
	data, err := os.ReadFile(actual)
	require.NoError(t, err)
	require.Equal(t, "first", string(data))
	_, err = writeBatchDownload(t.Context(), path, io.NopCloser(strings.NewReader("replacement")))
	require.Error(t, err)
	data, err = os.ReadFile(actual)
	require.NoError(t, err)
	require.Equal(t, "first", string(data))
	_, err = os.Stat(filepath.Join(directory, "output.jsonl"))
	require.ErrorIs(t, err, os.ErrNotExist)
}
