package custom

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func pickerOpenFolder(t *testing.T, m *imagePicker) {
	t.Helper()
	m.focus = "options"
	for i, row := range m.rows() {
		if row.id == "folder" {
			m.selected = i
			pickerKey(m, tea.KeyEnter)
			require.Equal(t, "folder", m.page)
			return
		}
	}
	t.Fatal("Save to row not found")
}

func pickerOpenPath(t *testing.T, m *imagePicker) {
	t.Helper()
	pickerOpenFolder(t, m)
	m.selected = 2
	pickerKey(m, tea.KeyEnter)
	require.Equal(t, "path", m.focus)
}

func TestImagePickerRestoredSettingsAndExplicitPrompt(t *testing.T) {
	original := pickerForTest(t).settings
	original.prompt, original.quality, original.outputDir = "Remembered 雪", "high", t.TempDir()
	for _, prompt := range []string{"", "Override 雪"} {
		m, err := newImagePicker(imagePickerOptions{initial: &original, initialNote: "Remembered settings", Prompt: prompt})
		require.NoError(t, err)
		require.Equal(t, prompt, m.settings.prompt)
		require.Equal(t, prompt, string(m.draft))
		require.Equal(t, len([]rune(prompt)), m.cursor)
		require.Equal(t, original.outputDir, m.settings.outputDir)
		require.Equal(t, "high", m.settings.quality)
		require.Equal(t, "Remembered settings", m.note)
		require.Equal(t, "Remembered 雪", original.prompt)
	}
}

func TestImagePickerSaveFolder(t *testing.T) {
	m := pickerForTest(t)
	for _, size := range [][2]int{{40, 12}, {80, 24}} {
		m.width, m.height = size[0], size[1]
		// Short terminals scroll settings; focusing the folder keeps it visible.
		m.focus = "options"
		for i, row := range m.rows() {
			if row.id == "folder" {
				m.selected = i
			}
		}
		require.Contains(t, ansi.Strip(m.View().Content), "Save to")
		require.Contains(t, ansi.Strip(m.View().Content), "Ctrl+C exit")
	}
	m.width, m.height = 90, 24
	require.Equal(t, "more", m.rows()[len(m.rows())-1].id, "End still opens More options")
	pickerOpenFolder(t, m)
	require.Equal(t, []string{"Default", "Current folder", "Enter path", "Back to settings"}, []string{m.rows()[0].label, m.rows()[1].label, m.rows()[2].label, m.rows()[3].label})
	m.selected = 1
	pickerKey(m, tea.KeyEnter)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, cwd, m.settings.outputDir)
	require.Equal(t, "settings", m.page)
	require.Contains(t, ansi.Strip(m.View().Content), "Save to")
	require.Equal(t, cwd, pickerArg(t, m.settings.args(), "--output-dir"))
	pickerOpenFolder(t, m)
	pickerKey(m, tea.KeyEnter) // Default.
	require.Empty(t, m.settings.outputDir)
	require.NotContains(t, m.settings.args(), "--output-dir")
}

func TestImagePickerFolderEditorPreservesLiteralPath(t *testing.T) {
	m := pickerForTest(t)
	t.Chdir(t.TempDir())
	literal := " @'$HOME' 雪 folder "
	require.NoError(t, os.Mkdir(literal, 0700))
	m.settings.outputDir = t.TempDir()
	pickerOpenPath(t, m)
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m.Update(tea.PasteMsg{Content: literal})
	require.Equal(t, literal, string(m.folder.draft))
	require.Empty(t, m.result.Args)
	pickerKey(m, tea.KeyEnter)
	want, err := filepath.Abs(literal)
	require.NoError(t, err)
	require.Equal(t, want, m.settings.outputDir)
	require.Empty(t, m.result.Args, "accepting a folder never generates")
	require.Equal(t, "settings", m.page)
	command := formatImagePickerCommandBash(m.settings.args())
	require.Contains(t, command, "--output-dir '")
	require.Equal(t, want, pickerArg(t, m.settings.args(), "--output-dir"))
}

func TestImagePickerFolderEscapeDiscardsDraftAndHighlight(t *testing.T) {
	m := pickerForTest(t)
	original := t.TempDir()
	m.settings.outputDir = original
	pickerOpenPath(t, m)
	m.Update(tea.PasteMsg{Content: "uncommitted"})
	pickerKey(m, tea.KeyEscape)
	require.Equal(t, "folder", m.page)
	require.Equal(t, original, m.settings.outputDir)
	m.selected = 0 // Highlight Default without accepting it.
	pickerKey(m, tea.KeyEscape)
	require.Equal(t, "settings", m.page)
	require.Equal(t, original, m.settings.outputDir)
	require.Empty(t, m.result.Args)
}

func TestImagePickerFolderCompletionDirectoriesOnly(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"雪 first", "雪 second", "$HOME"} {
		require.NoError(t, os.Mkdir(filepath.Join(root, name), 0700))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "雪 file"), []byte("synthetic"), 0600))
	require.NoError(t, os.Symlink(filepath.Join(root, "雪 first"), filepath.Join(root, "雪 linked")))
	matches, err := imagePickerCompleteFolders(context.Background(), filepath.Join(root, "雪"))
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(root, "雪 first") + string(filepath.Separator), filepath.Join(root, "雪 linked") + string(filepath.Separator), filepath.Join(root, "雪 second") + string(filepath.Separator)}, matches)
	matches, err = imagePickerCompleteFolders(context.Background(), filepath.Join(root, "$H"))
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(root, "$HOME") + string(filepath.Separator)}, matches)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = imagePickerCompleteFolders(ctx, root+string(filepath.Separator))
	require.ErrorIs(t, err, context.Canceled)
}

func TestImagePickerFolderCompletionRejectsFIFO(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix FIFO semantics")
	}
	mkfifo, err := exec.LookPath("mkfifo")
	require.NoError(t, err)
	for _, linked := range []bool{false, true} {
		name := "direct"
		if linked {
			name = "symlink"
		}
		t.Run(name, func(t *testing.T) {
			fifo := filepath.Join(t.TempDir(), "not-a-folder")
			require.NoError(t, exec.Command(mkfifo, fifo).Run())
			path := fifo
			if linked {
				path += "-link"
				require.NoError(t, os.Symlink(fifo, path))
			}
			done := make(chan error, 1)
			go func() {
				_, err := imagePickerCompleteFolders(t.Context(), path+string(filepath.Separator))
				done <- err
			}()
			select {
			case err := <-done:
				require.Error(t, err, "a FIFO is not a completion directory")
			case <-time.After(time.Second):
				// Release a blocked reader so a failing regression leaves no worker.
				writer, err := os.OpenFile(fifo, os.O_RDWR|syscall.O_NONBLOCK, 0)
				require.NoError(t, err)
				defer writer.Close()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("folder completion stayed blocked after releasing the FIFO")
				}
				t.Fatal("folder completion blocked opening a FIFO")
			}
		})
	}
}

func TestImagePickerFolderCompletionNeedsAcceptance(t *testing.T) {
	m := pickerForTest(t)
	root := t.TempDir()
	for _, name := range []string{"alpha", "alpine"} {
		require.NoError(t, os.Mkdir(filepath.Join(root, name), 0700))
	}
	pickerOpenPath(t, m)
	prefix := filepath.Join(root, "al")
	m.insertFolderPath(prefix)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	require.NotNil(t, cmd)
	m.Update(cmd())
	require.Len(t, m.folder.matches, 2)
	require.Equal(t, prefix, string(m.folder.draft))
	pickerKey(m, tea.KeyTab)
	require.Zero(t, m.folder.match)
	require.Equal(t, prefix, string(m.folder.draft), "highlight is not a draft edit")
	pickerKey(m, tea.KeyEnter)
	require.Equal(t, filepath.Join(root, "alpha")+string(filepath.Separator), string(m.folder.draft))
	require.Equal(t, "path", m.page)
	require.Empty(t, m.settings.outputDir)
	pickerKey(m, tea.KeyEnter)
	require.Equal(t, filepath.Join(root, "alpha"), m.settings.outputDir)
	require.Empty(t, m.result.Args)
}

func TestImagePickerFolderCompletionIgnoresLateReplies(t *testing.T) {
	m := pickerForTest(t)
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "only"), 0700))
	pickerOpenPath(t, m)
	m.insertFolderPath(root + string(filepath.Separator))
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	require.NotNil(t, cmd)
	reply := cmd()
	m.insertFolderPath("new draft")
	want := string(m.folder.draft)
	m.Update(reply)
	require.Equal(t, want, string(m.folder.draft))
	require.Empty(t, m.folder.matches)
	m.openFolderEditor(root + string(filepath.Separator))
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m.Update(cmd())
	require.Equal(t, filepath.Join(root, "only")+string(filepath.Separator), string(m.folder.draft))
	require.Empty(t, m.settings.outputDir, "unique completion edits draft only")
}

func TestImagePickerInvalidFolderRetainsEditorAndEscapesErrors(t *testing.T) {
	for _, invalid := range []string{"", "missing\x1b]52;c;injection\a\nnext", "nul\x00path", "not-a-folder"} {
		m := pickerForTest(t)
		t.Chdir(t.TempDir())
		require.NoError(t, os.WriteFile("not-a-folder", []byte("synthetic"), 0600))
		original := t.TempDir()
		m.settings.outputDir = original
		pickerOpenPath(t, m)
		m.openFolderEditor(invalid)
		pickerKey(m, tea.KeyEnter)
		require.Equal(t, "path", m.page)
		require.Equal(t, invalid, string(m.folder.draft))
		require.Equal(t, original, m.settings.outputDir)
		require.NotEmpty(t, m.note)
		require.Empty(t, m.result.Args)
		view := m.View().Content
		require.NotContains(t, view, "\x1b]")
		require.NotContains(t, view, "\a")
	}
}

func TestImagePickerFolderValidationBeforeSubmit(t *testing.T) {
	for _, printOnly := range []bool{false, true} {
		m := pickerForTest(t)
		m.settings.outputDir = t.TempDir()
		want := m.settings
		cmd := m.submitWithFolder(printOnly)
		require.NotNil(t, cmd)
		require.Empty(t, m.result.Args)
		_, quit := m.Update(cmd())
		require.NotNil(t, quit)
		require.IsType(t, tea.QuitMsg{}, quit())
		require.Equal(t, want, m.result.settings)
		require.Equal(t, want.args(), m.result.Args)
		require.Equal(t, printOnly, m.result.PrintOnly)
		entries, err := os.ReadDir(want.outputDir)
		require.NoError(t, err)
		require.Empty(t, entries, "write probe must be removed")
	}
}

func TestImagePickerDefaultFolderPrintHasNoSideEffects(t *testing.T) {
	m := pickerForTest(t)
	path := filepath.Join(os.Getenv("HOME"), "Downloads", "gpt-images")
	_, err := os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	require.True(t, m.result.PrintOnly)
	require.Empty(t, m.result.settings.outputDir)
	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestImagePickerDefaultFolderFailureCanBeCorrected(t *testing.T) {
	m := pickerForTest(t)
	require.NoError(t, os.WriteFile(filepath.Join(os.Getenv("HOME"), "Downloads"), []byte("synthetic"), 0600))
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	m.Update(cmd())
	require.Empty(t, m.result.Args)
	require.Equal(t, "path", m.page)
	require.Contains(t, m.note, "default image output")
	require.Empty(t, m.settings.outputDir)
	valid := t.TempDir()
	m.openFolderEditor(valid)
	pickerKey(m, tea.KeyEnter)
	require.Equal(t, valid, m.settings.outputDir)
	_, cmd = m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	cmd = pickerFinishFolderCommand(m, cmd)
	require.NotNil(t, cmd)
	require.Equal(t, valid, pickerArg(t, m.result.Args, "--output-dir"))
}

func TestImagePickerPendingFolderSubmissionCancellation(t *testing.T) {
	for _, cancel := range []tea.Msg{tea.KeyPressMsg{Code: tea.KeyEscape}, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, imagePickerStopMsg{code: 143}} {
		m := pickerForTest(t)
		m.settings.outputDir = t.TempDir()
		original := m.settings
		_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		require.NotNil(t, cmd)
		for _, queued := range []tea.Msg{tea.KeyPressMsg{Code: tea.KeyEnter}, tea.PasteMsg{Content: "do not apply"}, tea.KeyPressMsg{Code: 'x', Text: "x"}} {
			_, duplicate := m.Update(queued)
			require.Nil(t, duplicate)
		}
		require.Equal(t, original, m.settings)
		reply := cmd()
		m.Update(cancel)
		_, quit := m.Update(reply)
		require.Nil(t, quit)
		require.Empty(t, m.result.Args)
		require.Equal(t, original, m.settings)
	}
}

func TestImagePickerFolderSubmissionRefusesSmallResizeOrChangedState(t *testing.T) {
	for _, change := range []string{"resize", "settings"} {
		m := pickerForTest(t)
		m.settings.outputDir = t.TempDir()
		_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		reply := cmd()
		if change == "resize" {
			m.Update(tea.WindowSizeMsg{Width: 25, Height: 7})
		} else {
			m.settings.prompt = "new state"
		}
		_, quit := m.Update(reply)
		require.Nil(t, quit)
		require.Empty(t, m.result.Args)
	}
}

func TestImagePickerFolderViewBoundsAndVisibleExit(t *testing.T) {
	m := pickerForTest(t)
	pickerOpenPath(t, m)
	m.insertFolderPath(strings.Repeat("雪 folder /", 100))
	m.folder.matches = []string{"first", "second", "third", "fourth"}
	m.folder.match, m.selected = 3, 3
	m.note = "Folder error\x1b[31m\nnext"
	for _, size := range [][2]int{{40, 12}, {48, 14}, {80, 24}, {120, 30}} {
		m.width, m.height = size[0], size[1]
		view := m.View().Content
		require.Contains(t, view, "Ctrl+C exit")
		require.Contains(t, ansi.Strip(view), "› fourth")
		require.NotContains(t, view, "\x1b[31m")
		require.LessOrEqual(t, len(strings.Split(view, "\n")), m.viewHeight())
		for _, line := range strings.Split(view, "\n") {
			require.LessOrEqual(t, ansi.StringWidth(line), size[0])
		}
	}
}

func TestImagePickerReadOnlyFolderKeepsPickerOpen(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix permission checks for a non-root user")
	}
	m := pickerForTest(t)
	directory := t.TempDir()
	require.NoError(t, os.Chmod(directory, 0500))
	t.Cleanup(func() { require.NoError(t, os.Chmod(directory, 0700)) })
	m.settings.outputDir = directory
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	m.Update(cmd())
	require.Empty(t, m.result.Args)
	require.Equal(t, "path", m.page)
	require.Equal(t, directory, string(m.folder.draft))
	require.Contains(t, m.note, "not writable")
}

func TestImagePickerFolderLabelsAbbreviateOnlyHomeChildren(t *testing.T) {
	m := pickerForTest(t)
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	for _, tc := range []struct{ path, want string }{
		{home, "~"},
		{filepath.Join(home, "Pictures", "雪 folder"), "~/Pictures/雪 folder"},
		{home + "-neighbor", home + "-neighbor"},
		{filepath.Dir(home), filepath.Dir(home)},
		{"relative", "relative"},
	} {
		require.Equal(t, tc.want, imagePickerFolderLabel(tc.path))
	}
	path := filepath.Join(home, "Pictures", "雪 folder")
	m.settings.outputDir = path
	require.Equal(t, "~/Pictures/雪 folder", m.folderRow().value)
	require.Contains(t, ansi.Strip(m.View().Content), "~/Pictures/雪 folder")
	require.Equal(t, path, m.settings.outputDir)
	require.Equal(t, path, pickerArg(t, m.settings.args(), "--output-dir"))
}
