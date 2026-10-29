package batches

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestBatchBrowserSaveEditorShowsFolderForRelativeNames(t *testing.T) {
	actions := newBatchBrowserActions("batch_demo")
	actions.actionsState.saveFolder = filepath.Join(t.TempDir(), "exports")
	path := newBatchBrowserPath(actions, "output")
	path.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	for _, name := range []string{"", "nested/results.jsonl"} {
		path.input.SetValue(name)
		view := ansi.Strip(path.View().Content)
		require.Contains(t, view, "Folder:")
		require.Contains(t, view, "exports")
	}
	for _, name := range []string{filepath.Join(actions.actionsState.saveFolder, "results.jsonl"), "~/results.jsonl"} {
		path.input.SetValue(name)
		require.NotContains(t, ansi.Strip(path.View().Content), "Folder:")
	}
	require.Contains(t, ansi.Strip(path.View().Content), "Ctrl+L folder")
}

func TestBatchBrowserInlineSaveFolderAndDrafts(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	dir, err := os.Getwd()
	require.NoError(t, err)
	child := filepath.Join(dir, "exports")
	require.NoError(t, os.Mkdir(child, 0700))
	child, err = filepath.EvalSymlinks(child)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "not-a-folder.csv"), []byte("local"), 0600))
	actions := newBatchBrowserActions("batch_demo")
	actions.setSaveExpanded(true)
	step := 0
	output, err := chooseBatchBrowserSavePath(t.Context(), actions, "output", "batch-output.jsonl", func(_ context.Context, m *batchBrowserScreen) error {
		switch step {
		case 0:
			require.Contains(t, ansi.Strip(m.View().Content), "Ctrl+L folder")
			m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
		case 1:
			require.Equal(t, "Choose save folder", m.title)
			require.Len(t, m.choices, 2)
			require.Equal(t, "Use this folder", m.choices[0].title)
			m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		case 2:
			require.Contains(t, m.subtitle, "exports")
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		case 3:
			require.Equal(t, "path", m.kind)
			require.Equal(t, "batch-output.jsonl", m.input.Value())
			require.Equal(t, child, m.pathState.folder)
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		default:
			t.Fatalf("unexpected extra screen %s", m.kind)
		}
		step++
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 4, step)
	require.Equal(t, filepath.Join(child, "batch-output.jsonl"), output)
	require.NoFileExists(t, output)
	require.Equal(t, child, actions.actionsState.saveFolder)

	// Untouched choices follow the session folder. Renamed files keep their draft.
	choose := func(selection, suggested, field string, edit func(*batchBrowserScreen)) string {
		t.Helper()
		result, err := chooseBatchBrowserSavePath(t.Context(), actions, selection, suggested, func(_ context.Context, m *batchBrowserScreen) error {
			require.Equal(t, "path", m.kind)
			require.Equal(t, field, m.input.Value())
			edit(m)
			return nil
		})
		require.NoError(t, err)
		return result
	}
	require.Equal(t, filepath.Join(child, "errors.jsonl"), choose("error", "errors.jsonl", "errors.jsonl", func(m *batchBrowserScreen) { m.chosen = "save" }))
	require.Empty(t, choose("input", "input.jsonl", "input.jsonl", func(m *batchBrowserScreen) {
		m.input.SetValue("renamed.jsonl")
		m.chosen = "back"
	}))
	actions.actionsState.saveFolder = dir
	require.Equal(t, filepath.Join(dir, "errors.jsonl"), choose("error", "errors.jsonl", "errors.jsonl", func(m *batchBrowserScreen) { m.chosen = "save" }))
	require.Equal(t, filepath.Join(child, "renamed.jsonl"), choose("input", "input.jsonl", "renamed.jsonl", func(m *batchBrowserScreen) { m.chosen = "save" }))
}

func TestBatchBrowserCSVSaveFolderCancellationKeepsFilename(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	dir, err := os.Getwd()
	require.NoError(t, err)
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.Mkdir(sub, 0700))
	actions := newBatchBrowserActions("batch_demo")
	suggested := filepath.Join(dir, "tickets-results.csv")
	step := 0
	path, err := chooseBatchBrowserSavePath(t.Context(), actions, "csv", suggested, func(_ context.Context, m *batchBrowserScreen) error {
		switch step {
		case 0:
			require.Contains(t, ansi.Strip(m.View().Content), "Save CSV as")
			require.Equal(t, "tickets-results.csv", m.input.Value())
			require.NotContains(t, m.input.Value(), dir)
			m.input.SetValue("renamed.csv")
			m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
		case 1:
			m.Update(tea.PasteMsg{Content: "sub"})
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		case 2:
			require.Contains(t, m.subtitle, sub)
			m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
		case 3:
			require.Equal(t, "renamed.csv", m.input.Value())
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		default:
			t.Fatal("unexpected additional screen")
		}
		step++
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 4, step)
	require.Equal(t, filepath.Join(dir, "renamed.csv"), path)
	require.Empty(t, actions.actionsState.saveFolder)
	require.NoFileExists(t, path)
}

func TestBatchBrowserInlineSaveCorrectsOccupiedPathAndInvalidFolder(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "existing-file"), []byte("keep"), 0600))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "existing-folder"), 0700))
	occupied := []string{"existing-file", "existing-folder"}
	if runtime.GOOS != "windows" {
		require.NoError(t, os.Symlink("missing-target", filepath.Join(dir, "dangling-link")))
		occupied = append(occupied, "dangling-link")
	}
	for _, selection := range []string{"input", "output", "error", "csv"} {
		t.Run(selection, func(t *testing.T) {
			actions := newBatchBrowserActions("batch_demo")
			actions.setSaveExpanded(true)
			name := "report.jsonl"
			if selection == "csv" {
				name = "report.csv"
			}
			step := 0
			saved, err := chooseBatchBrowserSavePath(t.Context(), actions, selection, name, func(_ context.Context, m *batchBrowserScreen) error {
				switch step {
				case 0:
					for _, path := range occupied {
						m.input.SetValue(path)
						m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
						require.False(t, m.closed)
						require.Contains(t, m.notice, "already exists")
						require.Equal(t, path, m.input.Value())
					}
					for _, parent := range []string{"missing", "existing-file"} {
						typed := filepath.Join(parent, name)
						m.input.SetValue(typed)
						m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
						require.False(t, m.closed)
						require.Contains(t, m.notice, "existing folder")
						require.Equal(t, typed, m.input.Value())
					}
					m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
				case 1:
					require.Equal(t, "Choose save folder", m.title)
					require.Empty(t, m.notice)
					m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				case 2:
					require.Equal(t, name, m.input.Value())
					require.Empty(t, m.notice)
					m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				default:
					t.Fatal("unexpected screen")
				}
				step++
				return nil
			})
			require.NoError(t, err)
			require.Equal(t, 3, step)
			canonical, err := filepath.EvalSymlinks(dir)
			require.NoError(t, err)
			require.Equal(t, filepath.Join(canonical, name), saved)
			require.NoFileExists(t, saved)
			require.NoDirExists(t, filepath.Join(dir, "missing"))
		})
	}
}

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
