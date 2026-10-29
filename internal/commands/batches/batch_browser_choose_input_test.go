package batches

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
)

func batchInputChooseTitle(t *testing.T, m *batchBrowserScreen, title string) {
	t.Helper()
	for _, choice := range m.choices {
		if choice.title == title {
			m.chosen = choice.value
			return
		}
	}
	t.Fatalf("missing choice %q", title)
}

func TestBatchBrowserLocalInputBrowse(t *testing.T) {
	directory := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(directory, "z folder"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "a.jsonl"), []byte("{}\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "not-input.txt"), []byte("{}\n"), 0600))
	selected := filepath.Join(directory, "z folder", "résultats 日本語.jsonl")
	require.NoError(t, os.WriteFile(selected, []byte("{}\n"), 0600))
	step := 0
	path, err := chooseBatchBrowserFile(t.Context(), directory, "JSONL", func(ctx context.Context, m *batchBrowserScreen) error {
		step++
		require.Equal(t, "picker", m.flow.mode)
		require.Contains(t, m.subtitle, "Folder: ")
		if step == 1 {
			var names []string
			for _, choice := range m.choices {
				names = append(names, choice.title)
			}
			require.Equal(t, []string{"z folder" + string(filepath.Separator), "a.jsonl"}, names)
			batchInputChooseTitle(t, m, "z folder"+string(filepath.Separator))
		} else {
			require.Equal(t, 2, step)
			for _, r := range "résultats" {
				m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
			}
			require.Equal(t, "résultats 日本語.jsonl", m.choices[m.visible[0]].title)
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
		return nil
	})
	require.NoError(t, err)
	want, err := filepath.EvalSymlinks(selected)
	require.NoError(t, err)
	require.Equal(t, want, path)
}

func TestBatchBrowserUploadedInputRejectsMalformedPage(t *testing.T) {
	for _, body := range []string{
		`{"data":[],"has_more":true}`,
		`{"data":[{"filename":"missing-id.jsonl","purpose":"batch"}],"has_more":false}`,
		`{"data":[],"has_more":"false"}`,
		`{"data":{},"has_more":false}`,
		`{`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			client := openai.NewClient(option.WithAPIKey("synthetic-input-key"), option.WithBaseURL(server.URL), option.WithMaxRetries(0))
			page, err := fetchBatchBrowserUploadedPage(t.Context(), client, nil, "")
			require.Error(t, err)
			require.Empty(t, page.items)
			require.Empty(t, page.next)
		})
	}
}

func TestBatchBrowserLocalInputTypedPathRecoveryAndHome(t *testing.T) {
	directory := t.TempDir()
	filename := "typed input 日本語.jsonl"
	require.NoError(t, os.WriteFile(filepath.Join(directory, filename), []byte("{}\n"), 0600))
	homeVariable := "HOME"
	if runtime.GOOS == "windows" {
		homeVariable = "USERPROFILE"
	}
	t.Setenv(homeVariable, directory)
	step := 0
	path, err := chooseBatchBrowserFile(t.Context(), directory, "JSONL", func(ctx context.Context, m *batchBrowserScreen) error {
		step++
		switch step {
		case 1:
			m.chosen = "path"
		case 2:
			require.Equal(t, "entry", m.flow.mode)
			m.input.SetValue("~/missing.jsonl")
			m.chosen = "value"
		case 3:
			require.Contains(t, m.notice, "Could not read that location")
			require.Equal(t, "~/missing.jsonl", m.input.Value())
			m.input.SetValue("~/" + filename)
			m.chosen = "value"
		default:
			t.Fatal("unexpected selector screen")
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, filepath.Join(directory, filename), path)
}

func TestBatchBrowserLocalInputReadFailureAndCancel(t *testing.T) {
	step := 0
	path, err := chooseBatchBrowserFile(t.Context(), filepath.Join(t.TempDir(), "missing"), "JSONL", func(ctx context.Context, m *batchBrowserScreen) error {
		step++
		switch step {
		case 1:
			require.Contains(t, m.notice, "Could not read this folder")
			m.chosen = "path"
		case 2:
			m.chosen = "back"
		case 3:
			m.chosen = "back"
		default:
			t.Fatal("unexpected selector screen")
		}
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, path)
}

func TestBatchBrowserLocalInputRejectsDevices(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix device path")
	}
	step := 0
	path, err := chooseBatchBrowserFile(t.Context(), t.TempDir(), "JSONL", func(ctx context.Context, m *batchBrowserScreen) error {
		step++
		switch step {
		case 1:
			m.chosen = "path"
		case 2:
			m.input.SetValue(os.DevNull)
			m.chosen = "value"
		case 3:
			require.Contains(t, m.notice, "regular input file")
			m.chosen = "back"
		case 4:
			m.chosen = "back"
		default:
			t.Fatal("unexpected selector screen")
		}
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, path)
}

func TestBatchBrowserUploadedInputPagesAndConnection(t *testing.T) {
	var cursors []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/files", r.URL.Path)
		require.Equal(t, "batch", r.URL.Query().Get("purpose"))
		require.Equal(t, "50", r.URL.Query().Get("limit"))
		require.Equal(t, "desc", r.URL.Query().Get("order"))
		require.Equal(t, "synthetic-project", r.Header.Get("OpenAI-Project"))
		require.Equal(t, "synthetic", r.Header.Get("X-Selector-Test"))
		after := r.URL.Query().Get("after")
		cursors = append(cursors, after)
		w.Header().Set("Content-Type", "application/json")
		if after == "" {
			_, _ = io.WriteString(w, `{"data":[{"id":"file_one","filename":"first.jsonl","purpose":"batch"},{"id":"file_other","filename":"not-batch.jsonl","purpose":"assistants"}],"has_more":true}`)
		} else {
			require.Equal(t, "file_other", after)
			_, _ = io.WriteString(w, `{"data":[{"id":"file_two","filename":"second 日本語.jsonl","purpose":"batch"}],"has_more":false}`)
		}
	}))
	defer server.Close()
	client := openai.NewClient(option.WithAPIKey("synthetic-input-key"), option.WithBaseURL(server.URL), option.WithMaxRetries(0))
	step := 0
	id, name, err := chooseBatchBrowserUploadedFile(t.Context(), client, []option.RequestOption{
		option.WithProject("synthetic-project"), option.WithHeader("X-Selector-Test", "synthetic"),
		option.WithQuery("purpose", "wrong"), option.WithQuery("after", "wrong"),
	}, func(ctx context.Context, m *batchBrowserScreen) error {
		if m.flow.mode == "busy" {
			return nil
		}
		step++
		require.Contains(t, m.subtitle, "Search covers this page only")
		switch step {
		case 1, 3:
			require.Contains(t, m.subtitle, "Page 1: 1 file.")
			m.chosen = "next"
		case 2:
			require.Contains(t, m.subtitle, "Page 2: 1 file.")
			m.chosen = "previous"
		case 4:
			batchInputChooseTitle(t, m, "second 日本語.jsonl")
		default:
			t.Fatal("unexpected selector screen")
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, "file_two", id)
	require.Equal(t, "second 日本語.jsonl", name)
	require.Equal(t, []string{"", "file_other", "", "file_other"}, cursors)
}

func TestBatchBrowserUploadedInputFailureAllowsManualID(t *testing.T) {
	for _, status := range []int{401, 403, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"message":"DO_NOT_SHOW_RAW_BODY","type":"invalid_request_error"}}`)
			}))
			defer server.Close()
			client := openai.NewClient(option.WithAPIKey("synthetic-input-key"), option.WithBaseURL(server.URL), option.WithMaxRetries(0))
			id, name, err := chooseBatchBrowserUploadedFile(t.Context(), client, nil, func(ctx context.Context, m *batchBrowserScreen) error {
				if m.flow.mode == "busy" {
					return nil
				}
				require.NotContains(t, m.notice, "DO_NOT_SHOW_RAW_BODY")
				if m.flow.mode == "picker" {
					require.Contains(t, m.notice, "Could not list uploaded files")
					m.chosen = "manual"
				} else {
					m.input.SetValue("file_manual")
					m.chosen = "value"
				}
				return nil
			})
			require.NoError(t, err)
			require.Equal(t, "file_manual", id)
			require.Empty(t, name)
			require.Equal(t, 1, calls)
		})
	}
}

func TestBatchBrowserUploadedInputStopsRepeatedCursors(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		id := "file_one"
		if calls == 2 {
			id = "file_two"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"`+id+`","purpose":"batch"}],"has_more":true}`)
	}))
	defer server.Close()
	client := openai.NewClient(option.WithAPIKey("synthetic-input-key"), option.WithBaseURL(server.URL), option.WithMaxRetries(0))
	step := 0
	id, _, err := chooseBatchBrowserUploadedFile(t.Context(), client, nil, func(ctx context.Context, m *batchBrowserScreen) error {
		if m.flow.mode == "busy" {
			return nil
		}
		step++
		if step <= 3 {
			m.chosen = "next"
		} else {
			require.Equal(t, 4, step)
			require.Contains(t, m.notice, "repeated")
			for _, choice := range m.choices {
				require.NotEqual(t, "next", choice.value)
			}
			m.chosen = "back"
		}
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, id)
	require.Equal(t, 3, calls)
}

func TestBatchBrowserUploadedInputPropagatesTerminalFailure(t *testing.T) {
	client := openai.NewClient(option.WithAPIKey("synthetic-input-key"), option.WithBaseURL("http://127.0.0.1:1"), option.WithMaxRetries(0))
	failure := errors.New("synthetic terminal failure")
	calls := 0
	_, _, err := chooseBatchBrowserUploadedFile(t.Context(), client, nil, func(ctx context.Context, m *batchBrowserScreen) error {
		calls++
		return failure
	})
	require.ErrorIs(t, err, failure)
	require.Equal(t, 1, calls)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err = chooseBatchBrowserUploadedFile(ctx, client, nil, func(context.Context, *batchBrowserScreen) error {
		t.Fatal("rendered after cancellation")
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
}

func TestBatchBrowserInputLocationPreservesExplicitComponents(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, path := range []string{".", "..", "link/../input.jsonl", "folder/"} {
		resolved, err := batchBrowserInputLocation(path, "")
		require.NoError(t, err)
		require.True(t, filepath.IsAbs(resolved))
		require.True(t, strings.HasSuffix(resolved, path))
	}
}

func TestBatchBrowserInputPickerCombinesFormatsAndRemembersFolder(t *testing.T) {
	base := t.TempDir()
	base, err := filepath.EvalSymlinks(base)
	require.NoError(t, err)
	sub := filepath.Join(base, "data")
	require.NoError(t, os.Mkdir(sub, 0700))
	for _, name := range []string{"labels.CSV", "requests.jsonl", "hidden.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(sub, name), []byte("fixture"), 0600))
	}
	folder, step := base, 0
	var expected string
	selected, uploaded, err := browseBatchBrowserFolder(t.Context(), &folder, "", func(_ context.Context, m *batchBrowserScreen) error {
		step++
		require.Equal(t, "Choose input file", m.title)
		switch step {
		case 1:
			require.True(t, m.flow.localFiles)
			batchInputChooseTitle(t, m, "data"+string(filepath.Separator))
		case 2:
			require.Equal(t, "Folder: "+batchBrowserFolderLabel(sub), m.subtitle)
			var names []string
			for _, choice := range m.choices {
				names = append(names, choice.title)
			}
			require.Equal(t, []string{"Choose an uploaded file", "labels.CSV", "requests.jsonl"}, names)
			require.Equal(t, "labels.CSV", m.choices[m.visible[m.index]].title)
			m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
			m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
			require.False(t, m.closed)
			require.Equal(t, "labels.CSV", m.choices[m.visible[m.index]].title)
			m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
			require.Equal(t, "parent", m.chosen)
		case 3:
			require.Equal(t, "data"+string(filepath.Separator), m.choices[m.visible[m.index]].title)
			m.Update(tea.PasteMsg{Content: "data/labels.CSV"})
			require.Empty(t, m.visible)
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			require.Equal(t, "typed-path", m.chosen)
			expected = filepath.Join(sub, "labels.CSV")
		default:
			t.Fatal("unexpected picker screen")
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, expected, filepath.Clean(selected))
	require.False(t, uploaded)
	require.Equal(t, base, folder)
	_, uploaded, err = browseBatchBrowserFolder(t.Context(), &folder, "", func(_ context.Context, m *batchBrowserScreen) error {
		m.Update(tea.PasteMsg{Content: "uploaded"})
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		require.Equal(t, "uploaded", m.chosen)
		return nil
	})
	require.NoError(t, err)
	require.True(t, uploaded)
}

func TestBatchBrowserPathShortcutUsesVisibleFolder(t *testing.T) {
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	folder := filepath.Join(root, "nested")
	require.NoError(t, os.Mkdir(folder, 0700))
	target := filepath.Join(root, "original.csv")
	require.NoError(t, os.WriteFile(target, []byte("fixture"), 0600))
	t.Chdir(t.TempDir()) // Relative paths must resolve from the displayed folder.
	step := 0
	path, _, err := browseBatchBrowserFolder(t.Context(), &folder, "", func(_ context.Context, m *batchBrowserScreen) error {
		step++
		if step == 1 {
			m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
			require.Equal(t, "path", m.chosen)
		} else {
			require.Equal(t, "Folder: "+batchBrowserFolderLabel(folder), m.subtitle)
			m.Update(tea.PasteMsg{Content: "../original.csv"})
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(target), filepath.Clean(path))
}

func TestBatchBrowserPastedPathCorrection(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "more")
	require.NoError(t, os.Mkdir(nested, 0700))
	target := filepath.Join(nested, "answers.csv")
	require.NoError(t, os.WriteFile(target, []byte("fixture"), 0600))
	step := 0
	file, _, err := browseBatchBrowserFolder(t.Context(), &root, "", func(_ context.Context, m *batchBrowserScreen) error {
		step++
		if step == 1 {
			m.Update(tea.PasteMsg{Content: "more/typo.csv"})
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		} else {
			require.Equal(t, "Go to file or folder", m.title)
			require.Contains(t, m.notice, "Check its name")
			require.Equal(t, "more/typo.csv", m.input.Value())
			m.input.SetValue("more/answers.csv")
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
		return nil
	})
	require.NoError(t, err)
	expected, err := filepath.EvalSymlinks(target)
	require.NoError(t, err)
	require.Equal(t, expected, filepath.Clean(file))
}

func TestBatchBrowserSelectedFilenameRetainsControlCharacters(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filenames cannot contain newlines.")
	}
	folder := t.TempDir()
	name := "two\nlines.jsonl"
	target := filepath.Join(folder, name)
	require.NoError(t, os.WriteFile(target, []byte("{}\n"), 0600))
	path, _, err := browseBatchBrowserFolder(t.Context(), &folder, "", func(_ context.Context, m *batchBrowserScreen) error {
		batchInputChooseTitle(t, m, name)
		return nil
	})
	require.NoError(t, err)
	expected, err := filepath.EvalSymlinks(target)
	require.NoError(t, err)
	require.Equal(t, expected, path)
}
