package batches

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
)

// The save resolver supplies home expansion and an absolute location. Appending
// a temporary component permits folder operands such as "." and "~/" without
// changing the save field's filename validation. No component is created.
func batchBrowserInputLocation(value, folder string) (string, error) {
	if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", batchError("Enter a file or folder path.")
	}
	homePath := value == "~" || len(value) >= 2 && value[0] == '~' && os.IsPathSeparator(value[1])
	if folder != "" && !homePath && filepath.VolumeName(value) == "" && !os.IsPathSeparator(value[0]) {
		// Preserve symlink/.. traversal in user paths. filepath.Join would clean it away.
		if !os.IsPathSeparator(folder[len(folder)-1]) {
			folder += string(filepath.Separator)
		}
		return folder + value, nil
	}
	suffix := string(filepath.Separator) + ".batch-picker-location"
	path, err := batchBrowserSaveDestination(value + suffix)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(path, suffix), nil
}

func batchBrowserFolderLabel(folder string) string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		if folder == home {
			return "~"
		}
		if strings.HasPrefix(folder, home+string(filepath.Separator)) {
			return "~" + folder[len(home):]
		}
	}
	return folder
}

// Resolve the parent as the filesystem does, including symlink/.. traversal.
func batchBrowserFileFolder(path string) string {
	parent, _ := filepath.Split(path)
	if folder, err := filepath.EvalSymlinks(parent); err == nil {
		return folder
	}
	return parent
}

func batchBrowserTypedLocation(value, folder string) (string, bool, error) {
	path, err := batchBrowserInputLocation(value, folder)
	if err != nil {
		return "", false, err
	}
	return batchBrowserLocalEntry(path)
}

func batchBrowserLocalEntry(path string) (string, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", false, batchError("Could not read that location. Check its name, folder, and permissions.")
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return "", false, batchError("Choose a regular input file or folder. Devices and pipes cannot be uploaded here.")
	}
	return path, info.IsDir(), nil
}

func batchBrowserInputEntry(ctx context.Context, title, subtitle, prompt, value, notice string, show func(context.Context, *batchBrowserScreen) error) (string, bool, error) {
	m := newBatchBrowserFlow("entry", title, subtitle, nil)
	m.input.Prompt = prompt
	m.input.SetValue(value)
	m.notice = notice
	if err := show(ctx, m); err != nil {
		return "", false, err
	}
	return m.input.Value(), m.chosen != "back", ctx.Err()
}

func chooseBatchBrowserFile(ctx context.Context, initialDirectory, format string, show func(context.Context, *batchBrowserScreen) error) (string, error) {
	path, _, err := browseBatchBrowserFolder(ctx, &initialDirectory, format, show)
	return path, err
}

// The Create form offers both local formats and uploaded inputs. CSV export only needs a local CSV.
func browseBatchBrowserFolder(ctx context.Context, folder *string, format string, show func(context.Context, *batchBrowserScreen) error) (string, bool, error) {
	initialDirectory := *folder
	accepts := func(name string) bool {
		if format == "folder" {
			return false
		}
		ext := filepath.Ext(name)
		if format == "" {
			return strings.EqualFold(ext, ".csv") || strings.EqualFold(ext, ".jsonl")
		}
		return strings.EqualFold(ext, "."+format)
	}
	if initialDirectory == "" {
		initialDirectory = "."
	}
	directory := initialDirectory
	if !filepath.IsAbs(directory) {
		var err error
		directory, err = batchBrowserInputLocation(initialDirectory, "")
		if err != nil {
			return "", false, err
		}
	}
	draft, notice, highlightPath := "", "", ""
	for {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		choices := []batchBrowserChoice{}
		title := "Choose a " + format + " file"
		if format == "CSV" {
			title = "Choose the original CSV"
		}
		if format == "" {
			title = "Choose input file"
			choices = append(choices, batchBrowserChoice{value: "uploaded", title: "Choose an uploaded file", search: "Choose an uploaded file"})
		}
		if format == "folder" {
			title = "Choose save folder"
		}
		locations := map[string]string{}
		// Resolve existing folders before joining child names. Cleaning an
		// unresolved symlink/.. path would select a different file.
		canonical, readErr := filepath.EvalSymlinks(directory)
		var entries []os.DirEntry
		if readErr == nil {
			directory = canonical
			*folder = directory
			entries, readErr = os.ReadDir(directory)
		}
		if readErr != nil {
			notice = "Could not read this folder. Check folder access or enter another file or folder path."
		} else if format == "folder" {
			choices = append(choices, batchBrowserChoice{value: "use-folder", title: "Use this folder", search: "Use this folder"})
		}
		type localEntry struct {
			name, path string
			folder     bool
		}
		var available []localEntry
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return "", false, err
			}
			if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 && !accepts(entry.Name()) {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			info, statErr := os.Stat(path)
			if statErr != nil {
				continue
			}
			if info.IsDir() || info.Mode().IsRegular() && accepts(entry.Name()) {
				available = append(available, localEntry{entry.Name(), path, info.IsDir()})
			}
		}
		sort.SliceStable(available, func(i, j int) bool { return available[i].folder && !available[j].folder })
		for index, entry := range available {
			key := "local:" + strconv.Itoa(index)
			name := entry.name
			if entry.folder {
				name += string(filepath.Separator)
			}
			choices = append(choices, batchBrowserChoice{value: key, title: name, search: entry.name})
			locations[key] = entry.path
		}
		m := newBatchBrowserFlow("picker", title, "Folder: "+batchBrowserFolderLabel(directory), choices)
		if format == "" && len(available) > 0 {
			// Keep uploaded files visible above the list while focusing a local entry.
			m.index = 1
		}
		m.flow.localFiles = true
		m.input.Prompt = "Search  "
		m.input.Placeholder = "Filter this folder"
		m.notice = notice
		for i, choice := range choices {
			if highlightPath != "" && locations[choice.value] == highlightPath {
				m.index = i
				break
			}
		}
		highlightPath = ""
		if err := show(ctx, m); err != nil {
			return "", false, err
		}
		if m.chosen == "back" {
			return "", false, nil
		}
		notice = ""
		switch m.chosen {
		case "use-folder":
			return directory, false, nil
		case "uploaded":
			if format == "" {
				return "", true, nil
			}
			continue
		case "parent":
			highlightPath, directory, draft = directory, filepath.Dir(directory), ""
			continue
		case "refresh":
			continue
		}
		path := locations[m.chosen]
		var isFolder bool
		var err error
		if m.chosen == "path" || m.chosen == "typed-path" {
			editPath := m.chosen == "path"
			if !editPath {
				draft = m.input.Value()
			}
			for {
				if editPath {
					title := "Go to file or folder"
					if format == "folder" {
						title = "Go to folder"
					}
					value, selected, err := batchBrowserInputEntry(ctx, title, "Folder: "+batchBrowserFolderLabel(directory), "Path: ", draft, notice, show)
					if err != nil {
						return "", false, err
					}
					draft = value
					if !selected {
						notice = ""
						break
					}
				}
				path, isFolder, err = batchBrowserTypedLocation(draft, directory)
				if err == nil && format == "folder" && !isFolder {
					err = batchError("That location is a file. Enter a folder instead.")
					path = ""
				}
				if err == nil {
					break
				}
				notice, editPath = err.Error(), true
			}
		} else {
			// A listed entry may disappear between listing and selection.
			path, isFolder, err = batchBrowserLocalEntry(path)
			if err != nil {
				notice = err.Error()
				continue
			}
		}
		if path == "" {
			continue
		}
		if isFolder {
			directory, draft = path, ""
			continue
		}
		return path, false, nil
	}
}

func fetchBatchBrowserUploadedPage(ctx context.Context, client openai.Client, options []option.RequestOption, after string) (batchBrowsePage, error) {
	var raw []byte
	opts := append(options[:len(options):len(options)], option.WithQuery("purpose", "batch"), option.WithQuery("order", "desc"),
		option.WithQuery("limit", "50"), option.WithQueryDel("after"), option.WithResponseBodyInto(&raw))
	if after != "" {
		opts = append(opts, option.WithQuery("after", after))
	}
	if _, err := client.Files.List(ctx, openai.FileListParams{}, opts...); err != nil {
		return batchBrowsePage{}, err
	}
	if !gjson.ValidBytes(raw) {
		return batchBrowsePage{}, batchError("The API returned invalid uploaded-file JSON. Refresh files or enter a file ID.")
	}
	value := gjson.ParseBytes(raw)
	data, more := value.Get("data"), value.Get("has_more")
	if !value.IsObject() || !data.IsArray() || more.Type != gjson.True && more.Type != gjson.False {
		return batchBrowsePage{}, batchError("The API returned an unfamiliar uploaded-file list. Refresh files or enter a file ID.")
	}
	page, last, invalid := batchBrowsePage{}, "", false
	data.ForEach(func(_, item gjson.Result) bool {
		if ctx.Err() != nil {
			return false
		}
		id := item.Get("id")
		if !item.IsObject() || id.Type != gjson.String || id.Str == "" {
			invalid = true
			return false
		}
		last = id.Str
		if item.Get("purpose").Str == "batch" {
			page.items = append(page.items, item)
		}
		return true
	})
	if err := ctx.Err(); err != nil {
		return batchBrowsePage{}, err
	}
	if invalid {
		return batchBrowsePage{}, batchError("The API returned an uploaded file without an ID. Refresh files or enter a file ID.")
	}
	if more.Bool() {
		if last == "" || last == after {
			return batchBrowsePage{}, batchError("The API repeated its file page. Refresh files or enter a file ID.")
		}
		page.next = last
	}
	return page, nil
}

func batchBrowserUploadedReadNotice(err error) string {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		guidance := "Check the connection, refresh files, or enter a file ID."
		if apiErr.StatusCode == 401 {
			guidance = "Check this session's API key, or enter a file ID."
		} else if apiErr.StatusCode == 403 {
			guidance = "Check Files Read access for this project, or enter a file ID."
		}
		return fmt.Sprintf("Could not list uploaded files (HTTP %d). %s", apiErr.StatusCode, guidance)
	}
	var workflowErr *WorkflowError
	if errors.As(err, &workflowErr) {
		return workflowErr.Error()
	}
	return "Could not list uploaded files. Check the connection, refresh files, or enter a file ID."
}

func chooseBatchBrowserUploadedFile(ctx context.Context, client openai.Client, options []option.RequestOption, show func(context.Context, *batchBrowserScreen) error) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	cursors, position := []string{""}, 0
	page, notice, draft := batchBrowsePage{}, "", ""
	var terminalErr error
	load := func(after string) bool {
		showWork := func(ctx context.Context, m *batchBrowserScreen) error {
			terminalErr = show(ctx, m)
			return terminalErr
		}
		updated, err := batchBrowserWork(ctx, "Loading uploaded input files", showWork, func(ctx context.Context) (batchBrowsePage, error) {
			return fetchBatchBrowserUploadedPage(ctx, client, options, after)
		})
		if err != nil {
			notice = batchBrowserUploadedReadNotice(err)
			return false
		}
		page, notice = updated, ""
		return true
	}
	load("")
	for {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		if terminalErr != nil {
			return "", "", terminalErr
		}
		choices := []batchBrowserChoice{}
		for index, file := range page.items {
			name := file.Get("filename").Str
			if name == "" {
				name = file.Get("id").Str
			}
			choices = append(choices, batchBrowserChoice{value: "file:" + strconv.Itoa(index), title: name,
				detail: file.Get("id").Str, search: name + " " + file.Get("id").Str})
		}
		if position > 0 {
			choices = append(choices, batchBrowserChoice{value: "previous", title: "Previous page"})
		}
		if page.next != "" {
			choices = append(choices, batchBrowserChoice{value: "next", title: "Next page"})
		}
		choices = append(choices, batchBrowserChoice{value: "manual", title: "Enter a file ID"},
			batchBrowserChoice{value: "refresh", title: "Refresh files", detail: "Reload the first page."})
		countLabel := "files"
		if len(page.items) == 1 {
			countLabel = "file"
		}
		m := newBatchBrowserFlow("picker", "Choose an uploaded input file",
			fmt.Sprintf("Page %d: %d %s. Search covers this page only.", position+1, len(page.items), countLabel), choices)
		m.input.Placeholder = "Find a filename or file ID"
		m.notice = notice
		if err := show(ctx, m); err != nil {
			return "", "", err
		}
		switch m.chosen {
		case "back":
			return "", "", nil
		case "manual":
			for {
				value, selected, err := batchBrowserInputEntry(ctx, "Use an uploaded input file", "", "File ID: ", draft, notice, show)
				if err != nil {
					return "", "", err
				}
				draft = value
				if !selected {
					break
				}
				if value != "" && strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0 {
					return value, "", nil
				}
				notice = "Enter the uploaded file ID without spaces."
			}
		case "refresh":
			if load("") {
				cursors, position = []string{""}, 0
			}
		case "previous":
			if position > 0 && load(cursors[position-1]) {
				position--
			}
		case "next":
			if page.next == "" {
				continue
			}
			next, repeated := page.next, false
			for _, cursor := range cursors[:position+1] {
				repeated = repeated || cursor == next
			}
			if repeated {
				notice = "The API repeated its file page. Refresh files or enter a file ID."
				page.next = ""
			} else if load(next) {
				position++
				cursors = append(cursors[:position], next)
			}
		default:
			index, err := strconv.Atoi(strings.TrimPrefix(m.chosen, "file:"))
			if err == nil && index >= 0 && index < len(page.items) && strings.HasPrefix(m.chosen, "file:") {
				file := page.items[index]
				return file.Get("id").Str, file.Get("filename").Str, nil
			}
		}
	}
}
