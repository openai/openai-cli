package batches

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type batchBrowserPathState struct {
	folder string
}

func newBatchBrowserPath(actions *batchBrowserScreen, selection string) *batchBrowserScreen {
	m := newBatchBrowserScreen("path", actions.title, "", nil)
	m.actionsState = &batchBrowserActionsState{saveExpanded: true, csvSource: actions.actionsState.csvSource}
	m.pathState = &batchBrowserPathState{folder: actions.actionsState.saveFolder}
	m.setOverview(actions.actionsState.overview)
	if selection == "csv" {
		m.choices = []batchBrowserChoice{{value: "save:csv", title: "Save CSV as"}}
		m.filter()
	}
	for i, choice := range m.choices {
		if choice.value == "save:"+selection {
			m.index = i
			break
		}
	}
	m.input.Prompt = "File: "
	m.input.Placeholder = "Filename or path"
	m.input.SetValue(batchBrowserDefaultSaveName(actions.title, selection))
	return m
}

func (m *batchBrowserScreen) updatePath(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.SetWidth(max(1, msg.Width-20))
	case tea.KeyPressMsg:
		switch key := msg.String(); key {
		case "esc":
			m.chosen, m.closed = "back", true
			return m, tea.Quit
		case "ctrl+c":
			m.closed = true
			return m, tea.Quit
		case "up", "shift+tab", "down", "tab":
			if strings.TrimSpace(m.input.Value()) != "" {
				return m, nil
			}
			direction := 1
			if key == "up" || key == "shift+tab" {
				direction = -1
			}
			m.index = min(max(0, m.index+direction), max(0, len(m.visible)-1))
			m.notice, m.chosen, m.closed = "", "back", true
			return m, tea.Quit
		case "enter", "ctrl+l":
			if m.width < 30 || m.height < 7 {
				return m, nil
			}
			if key == "ctrl+l" {
				m.chosen = "folder"
			} else {
				if err := checkBatchBrowserSavePath(m.input.Value(), m.pathState.folder); err != nil {
					m.notice = err.Error()
					return m, nil
				}
				m.chosen = "save"
			}
			m.closed = true
			return m, tea.Quit
		}
	}
	previous := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(message)
	if m.input.Value() != previous {
		m.notice = ""
	}
	return m, cmd
}

func (m *batchBrowserScreen) pathKeys(shortcut func(string, string) string) []string {
	keys := []string{shortcut("Enter", "save"), shortcut("Ctrl+L", "folder"), shortcut("Esc", "back")}
	if strings.TrimSpace(m.input.Value()) == "" {
		keys = append([]string{shortcut("↑↓", "move")}, keys...)
	}
	return keys
}

func (m *batchBrowserScreen) compactPathFooter(width int, shortcut func(string, string) string) []string {
	line := "  " + shortcut("Enter", "save") + "  " + shortcut("Ctrl+L", "folder") + "  " + shortcut("Esc", "back")
	if ansi.StringWidth(line) > width {
		line = shortcut("Enter", "save") + " " + shortcut("^L", "folder") + " " + shortcut("Esc", "back")
	}
	return []string{line}
}

// The editor occupies the space beneath its selected save action.
func (m *batchBrowserScreen) pathLines(width int, muted, warning lipgloss.Style) ([]string, int) {
	promptWidth := ansi.StringWidth(m.input.Prompt)
	value, x := m.searchView(max(1, width-4-promptWidth))
	filename := m.input.Value()
	if filename == "" {
		value = muted.Render(value)
	}
	lines := []string{"      " + muted.Render(m.input.Prompt) + value}
	// A relative name needs folder context. A complete path already includes it.
	if !filepath.IsAbs(filename) && !(len(filename) > 1 && filename[0] == '~' && os.IsPathSeparator(filename[1])) {
		folder := m.pathState.folder
		if folder == "" {
			folder, _ = os.Getwd()
		}
		if folder != "" {
			label := batchInputLabel(batchBrowserFolderLabel(folder))
			size := max(1, width-4-ansi.StringWidth("Folder: "))
			if excess := ansi.StringWidth(label) - size; excess > 0 {
				label = ansi.TruncateLeft(label, excess+1, "…")
			}
			lines = append(lines, "      "+muted.Render("Folder: ")+muted.Render(label))
		}
	}
	if m.notice != "" {
		notice := ansi.Truncate(batchInputLabel(m.notice), width, "…")
		lines = append(lines, "  "+warning.Render(notice))
	}
	return lines, 6 + promptWidth + x
}

// Suggested names identify their batch without trusting API IDs as file paths.
func batchBrowserDefaultSaveName(batchID, selection string) string {
	if selection != "input" && selection != "output" && selection != "error" {
		selection = "file"
	}
	safe := batchID != "" && len(batchID) <= 128
	for _, r := range batchID {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			safe = false
			break
		}
	}
	if !safe {
		digest := sha256.Sum256([]byte(batchID))
		batchID = "batch_" + hex.EncodeToString(digest[:])
	}
	return batchID + "-" + selection + ".jsonl"
}

// Resolve only home and working-folder references. Keep Unix symlink/..
// components intact so the displayed destination matches filesystem resolution.
// This does not probe or create folders. The existing download writer owns
// no-overwrite publication and destination cleanup.
func batchBrowserSaveDestination(value string) (string, error) {
	return batchBrowserSaveDestinationIn(value, "")
}

func batchBrowserSaveDestinationIn(value, directory string) (string, error) {
	if strings.TrimSpace(value) == "" || value == "-" || value == "~" || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("Enter a filename or file path.")
	}
	if os.IsPathSeparator(value[len(value)-1]) || filepath.Base(value) == "." || filepath.Base(value) == ".." {
		return "", errors.New("Enter a filename after the folder path.")
	}
	if strings.HasPrefix(value, "~/") || (runtime.GOOS == "windows" && strings.HasPrefix(value, "~\\")) {
		home, err := os.UserHomeDir()
		if err != nil || !filepath.IsAbs(home) {
			return "", errors.New("Could not find your home folder. Enter an absolute file path.")
		}
		value = home + string(filepath.Separator) + filepath.FromSlash(value[2:])
	}
	if filepath.IsAbs(value) {
		return value, nil
	}
	if runtime.GOOS == "windows" && (filepath.VolumeName(value) != "" || os.IsPathSeparator(value[0])) {
		return filepath.Abs(value)
	}
	if directory == "" {
		var err error
		directory, err = os.Getwd()
		if err != nil {
			return "", errors.New("Could not find the current folder. Enter an absolute file path.")
		}
	}
	if !os.IsPathSeparator(directory[len(directory)-1]) {
		directory += string(filepath.Separator)
	}
	return directory + value, nil
}

type batchBrowserSaveDraft struct {
	name, folder string
}

// Report existing destinations while the filename is editable. The download
// writer still guards against files created after this check.
func checkBatchBrowserSavePath(value, directory string) error {
	dest, err := batchBrowserSaveDestinationIn(value, directory)
	if err != nil {
		return err
	}
	parent, _ := filepath.Split(dest)
	info, err := os.Stat(parent)
	if errors.Is(err, os.ErrPermission) {
		return errors.New("Cannot access that folder. Press Ctrl+L to choose another.")
	}
	if err != nil || !info.IsDir() {
		return errors.New("Choose an existing folder. Press Ctrl+L to browse.")
	}
	if _, err := os.Lstat(dest); err == nil {
		return errors.New("That name already exists. Enter another filename or press Ctrl+L to change folders.")
	}
	return nil
}

// The same inline editor handles all file types. Browsing only changes the
// destination; Enter on the filename confirms the write.
func chooseBatchBrowserSavePath(ctx context.Context, actions *batchBrowserScreen, selection, suggested string, show func(context.Context, *batchBrowserScreen) error) (string, error) {
	path := newBatchBrowserPath(actions, selection)
	draft, hasDraft := actions.actionsState.saveDrafts[selection]
	if hasDraft {
		path.input.SetValue(draft.name)
		path.pathState.folder = draft.folder
	} else {
		dir, name := filepath.Split(suggested)
		path.input.SetValue(name)
		if actions.actionsState.saveFolder == "" && dir != "" {
			var err error
			path.pathState.folder, err = batchBrowserInputLocation(dir, "")
			if err != nil {
				return "", err
			}
		}
	}
	initialName := path.input.Value()
	folderChosen := false
	for {
		if err := show(ctx, path); err != nil {
			return "", err
		}
		name := path.input.Value()
		if path.chosen == "folder" {
			start := path.pathState.folder
			if dest, err := batchBrowserSaveDestinationIn(name, start); err == nil {
				parent, _ := filepath.Split(dest)
				if info, err := os.Stat(parent); err == nil && info.IsDir() {
					start = parent
				}
			}
			if start != "" {
				if info, err := os.Stat(start); err != nil || !info.IsDir() {
					start = ""
				}
			}
			folder, _, err := browseBatchBrowserFolder(ctx, &start, "folder", show)
			if err != nil {
				return "", err
			}
			if folder != "" {
				path.pathState.folder, actions.actionsState.saveFolder = folder, folder
				folderChosen = true
				// A chosen folder replaces any parent in the edited filename.
				_, name = filepath.Split(name)
				path.input.SetValue(name)
				path.notice = ""
			}
			continue
		}
		// Untouched suggestions follow later folder choices. Edited destinations
		// retain their own name and folder when the user returns to that file.
		if hasDraft || folderChosen || name != initialName {
			if actions.actionsState.saveDrafts == nil {
				actions.actionsState.saveDrafts = make(map[string]batchBrowserSaveDraft)
			}
			actions.actionsState.saveDrafts[selection] = batchBrowserSaveDraft{name: name, folder: path.pathState.folder}
		}
		if path.chosen == "back" {
			if selection != "csv" {
				selected := path.choices[path.visible[path.index]].value
				for i, index := range actions.visible {
					if actions.choices[index].value == selected {
						actions.index = i
						break
					}
				}
			}
			return "", nil
		}
		if path.chosen != "save" {
			return "", ctx.Err()
		}
		dest, err := batchBrowserSaveDestinationIn(name, path.pathState.folder)
		if err != nil {
			return "", batchError(err.Error())
		}
		if dir, _ := filepath.Split(name); dir != "" {
			// Preserve unresolved symlink/.. components and never remember a missing folder.
			dir, _ = filepath.Split(dest)
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				actions.actionsState.saveFolder = dir
			}
		}
		return dest, nil
	}
}
