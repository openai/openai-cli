package custom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-cli/internal/imageoutput"
)

// Folder work runs outside the input loop. Revisions make late filesystem
// replies harmless after editing, backing out, or canceling the picker.
type imagePickerFolder struct {
	draft          []rune
	cursor         int
	matches        []string
	match          int
	returnPage     string
	returnSelected int
	revision       uint64
	busy           string
	cancel         context.CancelFunc
}

type imagePickerFolderMsg struct {
	revision   uint64
	kind, path string
	matches    []string
	settings   imagePickerSettings
	printOnly  bool
	err        error
}

func (m *imagePicker) folderRow() imagePickerRow {
	value := m.settings.outputDir
	if value == "" {
		value = "~/Downloads/gpt-images/ (Default)"
	} else {
		value = imagePickerFolderLabel(value)
	}
	return imagePickerRow{id: "folder", label: "Save to", value: value}
}

// Abbreviate only the visible label. Stored paths and command arguments stay
// absolute, and a neighboring directory with the same prefix stays unchanged.
func imagePickerFolderLabel(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(path) {
		return path
	}
	relative, err := filepath.Rel(home, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return path
	}
	if relative == "." {
		return "~"
	}
	return "~/" + filepath.ToSlash(relative)
}

func (m *imagePicker) folderRows() []imagePickerRow {
	return []imagePickerRow{
		{id: "folder-default", label: "Default", value: "~/Downloads/gpt-images/"},
		{id: "folder-current", label: "Current folder"},
		{id: "folder-path", label: "Enter path"},
		{id: "folder-back", label: "Back to settings"},
	}
}

func (m *imagePicker) folderCompletionRows() []imagePickerRow {
	rows := make([]imagePickerRow, 0, len(m.folder.matches))
	for _, path := range m.folder.matches {
		rows = append(rows, imagePickerRow{id: "folder-match", label: imagePickerLine(path)})
	}
	if len(rows) == 0 {
		rows = append(rows, imagePickerRow{label: "Tab completes directory names"})
	}
	return rows
}

func (m *imagePicker) activateFolder(id string) tea.Cmd {
	switch id {
	case "folder":
		m.folder.returnPage, m.folder.returnSelected = m.page, m.selected
		m.page, m.focus, m.selected, m.note = "folder", "options", 0, ""
	case "folder-default":
		m.commitFolder("")
	case "folder-current":
		return m.checkFolder("select", ".", false)
	case "folder-path":
		m.openFolderEditor(m.settings.outputDir)
	case "folder-back":
		return m.backFolder()
	}
	return nil
}

func (m *imagePicker) openFolderEditor(path string) {
	m.cancelFolderWork()
	m.page, m.focus, m.selected = "path", "path", 0
	m.folder.draft, m.folder.cursor = []rune(path), len([]rune(path))
	m.folder.match = -1
	m.note = ""
}

func (m *imagePicker) commitFolder(path string) {
	m.cancelFolderWork()
	m.settings.outputDir, m.commandOffset = path, 0
	m.page, m.focus, m.selected = m.folder.returnPage, "options", m.folder.returnSelected
	if m.page == "" {
		m.page = "settings"
	}
	m.note = ""
}

func (m *imagePicker) backFolder() tea.Cmd {
	m.cancelFolderWork()
	m.note = ""
	switch m.page {
	case "path":
		m.page, m.focus, m.selected = "folder", "options", 2
	case "folder":
		m.page, m.focus, m.selected = m.folder.returnPage, "options", m.folder.returnSelected
	default:
		m.focus = "prompt"
	}
	return nil
}

func (m *imagePicker) cancelFolderWork() {
	if m.folder.cancel != nil {
		m.folder.cancel()
		m.folder.cancel = nil
	}
	m.folder.revision++
	m.folder.busy = ""
	m.folder.matches, m.folder.match = nil, -1
}

func (m *imagePicker) folderWork(kind string) (context.Context, uint64) {
	m.cancelFolderWork()
	ctx, cancel := context.WithCancel(context.Background())
	m.folder.cancel, m.folder.busy = cancel, kind
	return ctx, m.folder.revision
}

func (m *imagePicker) insertFolderPath(text string) {
	m.cancelFolderWork()
	tail := append([]rune(nil), m.folder.draft[m.folder.cursor:]...)
	m.folder.draft = append(m.folder.draft[:m.folder.cursor], []rune(text)...)
	m.folder.draft = append(m.folder.draft, tail...)
	m.folder.cursor += len([]rune(text))
	m.note = ""
}

func (m *imagePicker) editFolderPath(key tea.KeyPressMsg) tea.Cmd {
	f := &m.folder
	switch key.String() {
	case "tab", "shift+tab":
		if f.busy != "" {
			return nil
		}
		if len(f.matches) > 0 {
			delta := 1
			if key.String() == "shift+tab" {
				delta = -1
			}
			f.match = (f.match + len(f.matches) + delta) % len(f.matches)
			m.selected = f.match
			return nil
		}
		path := string(f.draft)
		ctx, revision := m.folderWork("complete")
		m.note = "Finding folders…"
		return func() tea.Msg {
			matches, err := imagePickerCompleteFolders(ctx, path)
			return imagePickerFolderMsg{revision: revision, kind: "complete", path: path, matches: matches, err: err}
		}
	case "enter":
		if f.busy != "" {
			return nil
		}
		if f.match >= 0 && f.match < len(f.matches) {
			m.openFolderEditor(f.matches[f.match])
			return nil
		}
		return m.checkFolder("select", string(f.draft), false)
	case "up", "down":
		if len(f.matches) > 0 {
			if key.String() == "up" {
				f.match = max(0, f.match-1)
			} else {
				f.match = min(len(f.matches)-1, f.match+1)
			}
			m.selected = f.match
		}
	case "left":
		f.cursor = max(0, f.cursor-1)
	case "right":
		f.cursor = min(len(f.draft), f.cursor+1)
	case "home", "ctrl+a":
		f.cursor = 0
	case "end", "ctrl+e":
		f.cursor = len(f.draft)
	case "ctrl+u":
		m.cancelFolderWork()
		f.draft, f.cursor = f.draft[f.cursor:], 0
	case "backspace":
		m.cancelFolderWork()
		if f.cursor > 0 {
			f.draft = append(f.draft[:f.cursor-1], f.draft[f.cursor:]...)
			f.cursor--
		}
	case "delete":
		m.cancelFolderWork()
		if f.cursor < len(f.draft) {
			f.draft = append(f.draft[:f.cursor], f.draft[f.cursor+1:]...)
		}
	default:
		if key.Text != "" {
			m.insertFolderPath(key.Text)
		}
	}
	return nil
}

func (m *imagePicker) checkFolder(kind, path string, printOnly bool) tea.Cmd {
	ctx, revision := m.folderWork(kind)
	snapshot := m.settings
	m.note = "Checking save folder… Esc cancels"
	return func() tea.Msg {
		msg := imagePickerFolderMsg{revision: revision, kind: kind, path: path, settings: snapshot, printOnly: printOnly}
		if err := ctx.Err(); err != nil {
			msg.err = err
			return msg
		}
		if kind == "submit" {
			msg.path, msg.err = imageoutput.ResolveDirectory(path)
		} else {
			msg.path, msg.err = imagePickerExistingFolder(path)
		}
		if err := ctx.Err(); err != nil {
			msg.err = err
		}
		return msg
	}
}

func (m *imagePicker) submitWithFolder(printOnly bool) tea.Cmd {
	if printOnly && m.settings.outputDir == "" {
		return m.finishSubmission(m.settings, true)
	}
	return m.checkFolder("submit", m.settings.outputDir, printOnly)
}

func (m *imagePicker) finishSubmission(settings imagePickerSettings, printOnly bool) tea.Cmd {
	m.result = imagePickerResult{Args: settings.args(), PrintOnly: printOnly, settings: settings, shell: m.shell}
	return tea.Quit
}

func (m *imagePicker) finishFolderWork(msg imagePickerFolderMsg) tea.Cmd {
	if msg.revision != m.folder.revision || msg.kind != m.folder.busy {
		return nil
	}
	if m.folder.cancel != nil {
		m.folder.cancel()
		m.folder.cancel = nil
	}
	m.folder.busy = ""
	if errors.Is(msg.err, context.Canceled) {
		return nil
	}
	if msg.kind == "complete" {
		m.note = ""
		if msg.err != nil {
			m.note = "Could not read that folder."
			return nil
		}
		m.folder.matches, m.folder.match, m.selected = msg.matches, -1, 0
		if len(msg.matches) == 1 {
			m.openFolderEditor(msg.matches[0])
		} else if len(msg.matches) == 0 {
			m.note = "No matching folders."
		}
		return nil
	}
	if msg.kind == "submit" && msg.settings != m.settings {
		m.note = ""
		return nil
	}
	if msg.err != nil {
		path := string(m.folder.draft)
		if msg.kind == "submit" {
			path = msg.settings.outputDir
			if path == "" {
				path = "~/Downloads/gpt-images/"
			}
			m.folder.returnPage, m.folder.returnSelected = "settings", 0
		} else if m.page != "path" {
			path = "."
		}
		m.openFolderEditor(path)
		m.note = msg.err.Error()
		return nil
	}
	if msg.kind == "select" {
		m.commitFolder(msg.path)
		return nil
	}
	if m.width < 40 || m.height < 12 {
		m.note = "Resize before generating."
		return nil
	}
	// Empty retains default intent; an explicit path is stable across cwd changes.
	if msg.settings.outputDir != "" {
		msg.settings.outputDir = msg.path
	}
	m.settings = msg.settings
	return m.finishSubmission(msg.settings, msg.printOnly)
}

func imagePickerAbsoluteFolder(path string) (string, error) {
	if strings.ContainsRune(path, 0) {
		return "", errors.New("Remove the NUL character from the folder path.")
	}
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, path[2:])
		}
	}
	return filepath.Abs(path)
}

func imagePickerExistingFolder(path string) (string, error) {
	if path == "" {
		return "", errors.New("Enter an existing folder, or choose Default.")
	}
	absolute, err := imagePickerAbsoluteFolder(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("Choose an existing folder: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("Choose a folder, not a file.")
	}
	return absolute, nil
}

func imagePickerCompleteFolders(ctx context.Context, draft string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := imagePickerAbsoluteFolder(draft)
	if err != nil {
		return nil, err
	}
	parent, prefix := filepath.Dir(path), filepath.Base(path)
	if draft == "" || draft == "~" || os.IsPathSeparator(draft[len(draft)-1]) {
		parent, prefix = path, ""
	}
	// Inspect the opened handle: a path can be replaced by a FIFO between a
	// separate Stat and Open. Nonblocking open keeps that replacement from hanging.
	directory, err := os.OpenFile(parent, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	info, err := directory.Stat()
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("Choose a folder, not a file.")
	}
	var matches []string
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries, err := directory.ReadDir(128)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), prefix) {
				continue
			}
			candidate, isDir := filepath.Join(parent, entry.Name()), entry.IsDir()
			if entry.Type()&os.ModeSymlink != 0 {
				info, err := os.Stat(candidate)
				isDir = err == nil && info.IsDir()
			}
			if isDir {
				matches = append(matches, candidate+string(filepath.Separator))
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	sort.Strings(matches)
	return matches, nil
}
