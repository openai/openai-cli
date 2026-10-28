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
func batchBrowserInputLocation(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", batchError("Enter a file or folder path.")
	}
	suffix := string(filepath.Separator) + ".batch-picker-location"
	path, err := batchBrowserSaveDestination(value + suffix)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(path, suffix), nil
}

func batchBrowserInputEntry(ctx context.Context, title, prompt, value, notice string, show func(context.Context, *batchBrowserScreen) error) (string, bool, error) {
	m := newBatchBrowserFlow("entry", title, "", nil)
	m.input.Prompt = prompt
	m.input.SetValue(value)
	m.notice = notice
	if err := show(ctx, m); err != nil {
		return "", false, err
	}
	return m.input.Value(), m.chosen != "back", ctx.Err()
}

func chooseBatchBrowserLocalFile(ctx context.Context, initialDirectory string, show func(context.Context, *batchBrowserScreen) error) (string, error) {
	if initialDirectory == "" {
		initialDirectory = "."
	}
	directory, err := batchBrowserInputLocation(initialDirectory)
	if err != nil {
		return "", err
	}
	draft, notice := "", ""
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		choices := []batchBrowserChoice{}
		locations := map[string]string{}
		// Resolve existing folders before joining child names. Cleaning an
		// unresolved symlink/.. path would select a different file.
		canonical, readErr := filepath.EvalSymlinks(directory)
		var entries []os.DirEntry
		if readErr == nil {
			directory = canonical
			entries, readErr = os.ReadDir(directory)
		}
		if readErr != nil {
			notice = "Could not read this folder. Check folder access or enter another file or folder path."
		}
		type localEntry struct {
			name, path string
			folder     bool
		}
		var available []localEntry
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 && !strings.EqualFold(filepath.Ext(entry.Name()), ".jsonl") {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			info, statErr := os.Stat(path)
			if statErr != nil {
				continue
			}
			if info.IsDir() || info.Mode().IsRegular() && strings.EqualFold(filepath.Ext(entry.Name()), ".jsonl") {
				available = append(available, localEntry{entry.Name(), path, info.IsDir()})
			}
		}
		sort.SliceStable(available, func(i, j int) bool { return available[i].folder && !available[j].folder })
		if parent := filepath.Dir(directory); parent != directory {
			choices = append(choices, batchBrowserChoice{value: "parent", title: "Parent folder", detail: parent, search: "Parent folder"})
			locations["parent"] = parent
		}
		for index, entry := range available {
			key := "local:" + strconv.Itoa(index)
			name := entry.name
			if entry.folder {
				name += string(filepath.Separator)
			}
			choices = append(choices, batchBrowserChoice{value: key, title: name, detail: entry.path, search: entry.name})
			locations[key] = entry.path
		}
		choices = append(choices,
			batchBrowserChoice{value: "path", title: "Enter a file or folder path", detail: "Paste a path, including ~/ for your home folder."},
			batchBrowserChoice{value: "refresh", title: "Refresh folder"})
		m := newBatchBrowserFlow("picker", "Choose a JSONL input file", "Folder: "+directory, choices)
		m.input.Placeholder = "Find a folder or JSONL file"
		m.notice = notice
		if err := show(ctx, m); err != nil {
			return "", err
		}
		if m.chosen == "back" {
			return "", nil
		}
		notice = ""
		if m.chosen == "refresh" {
			continue
		}
		path := locations[m.chosen]
		if m.chosen == "path" {
			for {
				value, selected, inputErr := batchBrowserInputEntry(ctx, "Choose a local input file", "File or folder: ", draft, notice, show)
				if inputErr != nil {
					return "", inputErr
				}
				draft = value
				if !selected {
					path = ""
					break
				}
				path, inputErr = batchBrowserInputLocation(value)
				if inputErr == nil {
					break
				}
				notice = inputErr.Error()
			}
		}
		if path == "" {
			continue
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			notice = "Could not read that location. Check the path and access, or choose another file."
			continue
		}
		if info.IsDir() {
			directory = path
			continue
		}
		if !info.Mode().IsRegular() {
			notice = "Choose a regular input file. Folders, devices, and pipes cannot be uploaded here."
			continue
		}
		return path, nil
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
				value, selected, err := batchBrowserInputEntry(ctx, "Use an uploaded input file", "File ID: ", draft, notice, show)
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
