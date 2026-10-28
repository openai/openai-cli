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
	path, err := chooseBatchBrowserLocalFile(t.Context(), directory, func(ctx context.Context, m *batchBrowserScreen) error {
		step++
		require.Equal(t, "picker", m.flow.mode)
		require.Contains(t, m.subtitle, "Folder: ")
		if step == 1 {
			var names []string
			for _, choice := range m.choices {
				names = append(names, choice.title)
			}
			require.Equal(t, []string{"Parent folder", "z folder" + string(filepath.Separator), "a.jsonl", "Enter a file or folder path", "Refresh folder"}, names)
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
	path, err := chooseBatchBrowserLocalFile(t.Context(), directory, func(ctx context.Context, m *batchBrowserScreen) error {
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
			m.chosen = "path"
		case 4:
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
	path, err := chooseBatchBrowserLocalFile(t.Context(), filepath.Join(t.TempDir(), "missing"), func(ctx context.Context, m *batchBrowserScreen) error {
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
	path, err := chooseBatchBrowserLocalFile(t.Context(), t.TempDir(), func(ctx context.Context, m *batchBrowserScreen) error {
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
		resolved, err := batchBrowserInputLocation(path)
		require.NoError(t, err)
		require.True(t, filepath.IsAbs(resolved))
		require.True(t, strings.HasSuffix(resolved, path))
	}
}
