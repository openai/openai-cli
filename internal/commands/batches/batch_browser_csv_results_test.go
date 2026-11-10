package batches

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBatchCSVViewerOpensAndSavesVerifiedRows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tickets.csv")
	original := "id,message\n001,First\n002,\"Line one\nLine two\"\n003,=third\n004,Last\n"
	require.NoError(t, os.WriteFile(path, []byte(original), 0600))
	rows, err := csv.NewReader(strings.NewReader(original)).ReadAll()
	require.NoError(t, err)
	var inputs strings.Builder
	var ids []string
	for i, row := range rows[1:] {
		id := batchCSVRowID(rows[0], row, i+1)
		fmt.Fprintf(&inputs, "{\"custom_id\":%q}\n", id)
		ids = append(ids, id)
	}
	result := func(id, answer string) string {
		return fmt.Sprintf(`{"custom_id":%q,"response":{"status_code":200,"body":{"object":"response","output":[{"type":"message","content":[{"type":"output_text","text":%q}]}]}}}`+"\n", id, answer)
	}
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method %s", r.Method)
		}
		reads.Add(1)
		w.Header().Set("Content-Type", "application/jsonl")
		switch r.URL.Path {
		case "/files/source/content":
			fmt.Fprint(w, inputs.String())
		case "/files/answers/content":
			fmt.Fprint(w, result(ids[2], "=unsafe")+result(ids[0], "billing"))
		case "/files/errors/content":
			fmt.Fprintf(w, `{"custom_id":%q,"error":{"message":"Refused\u001b[2J"}}`, ids[1])
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := openai.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("synthetic-csv-view"))
	batch := gjson.Parse(`{"id":"batch_csv","status":"completed","input_file_id":"source","output_file_id":"answers","error_file_id":"errors"}`)
	defaultPath := filepath.Join(dir, "tickets-results.csv")
	copyPath := filepath.Join(dir, "renamed.csv")
	views, prompts := 0, 0
	err = browseBatchCSVResults(t.Context(), client, nil, batch, path, newBatchBrowserActions("batch_csv"), func(_ context.Context, m *batchBrowserScreen) error {
		switch m.title {
		case "Reading CSV results":
		case "batch_csv":
			require.Equal(t, "path", m.kind)
			prompts++
			require.Contains(t, m.notice, "already exists")
			if prompts == 1 {
				m.chosen = "back"
			} else {
				m.input.SetValue(copyPath)
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				require.Equal(t, "save", m.chosen)
			}
		case "Rows with answers":
			views++
			if views == 1 {
				_, err := os.Stat(defaultPath)
				require.ErrorIs(t, err, os.ErrNotExist)
				_, resize := m.Update(tea.WindowSizeMsg{Width: 60, Height: 13})
				applyBrowserDocumentCommand(t, m, resize)
				require.True(t, m.documentState.page.More)
				require.Contains(t, m.View().Content, "save CSV")
				var text strings.Builder
				require.NoError(t, m.document.writeTo(t.Context(), &text))
				expect := "Row 1\n  id: 001\n  message: First\nAnswer: billing\n\n" +
					"Row 2\n  id: 002\n  message: Line one\\nLine two\nError: Refused\\u001b[2J\n\n" +
					"Row 3\n  id: 003\n  message: =third\nAnswer: =unsafe\n\n" +
					"Row 4\n  id: 004\n  message: Last\nError: No result returned for this request.\n"
				require.Equal(t, expect, text.String())
				require.NoError(t, os.WriteFile(path, []byte("replaced after viewing"), 0600))
			}
			if views <= 3 {
				_, _ = m.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
				require.Equal(t, "save", m.chosen)
			} else {
				require.Contains(t, m.subtitle, "Saved renamed.csv")
				m.chosen = "back"
			}
		default:
			t.Fatalf("unexpected screen %s", m.title)
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 4, views)
	require.Equal(t, 2, prompts)
	require.EqualValues(t, 3, reads.Load())
	written, err := os.ReadFile(defaultPath)
	require.NoError(t, err)
	duplicate, err := os.ReadFile(copyPath)
	require.NoError(t, err)
	require.Equal(t, written, duplicate)
	resultRows, err := csv.NewReader(strings.NewReader(string(written))).ReadAll()
	require.NoError(t, err)
	require.Equal(t, []string{"003", "'=third", "'=unsafe", ""}, resultRows[3])
}

func TestBatchCSVViewerKeepsSaveAsFolderAfterReopening(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Exercises Unix symlink/.. traversal")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	target := filepath.Join(root, "actual")
	require.NoError(t, os.MkdirAll(filepath.Join(target, "child"), 0700))
	link := filepath.Join(root, "shortcut")
	require.NoError(t, os.Symlink(filepath.Join(target, "child"), link))
	path := filepath.Join(root, "tickets.csv")
	require.NoError(t, os.WriteFile(path, []byte("message\na\n"), 0600))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/files/input/content", r.URL.Path)
		fmt.Fprintf(w, "{\"custom_id\":%q}\n", batchCSVRowID([]string{"message"}, []string{"a"}, 1))
	}))
	defer server.Close()
	client := openai.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("synthetic-csv"))
	actions := newBatchBrowserActions("batch_csv")
	batch := gjson.Parse(`{"id":"batch_csv","status":"completed","input_file_id":"input"}`)
	for _, name := range []string{"first.csv", "tickets-results.csv"} {
		saving := true
		err := browseBatchCSVResults(t.Context(), client, nil, batch, path, actions, func(_ context.Context, m *batchBrowserScreen) error {
			switch m.title {
			case "Reading CSV results":
			case "Rows with answers":
				if saving {
					saving = false
					m.chosen = "save"
					if name == "first.csv" {
						m.chosen = "save-as"
					}
				} else {
					require.Equal(t, "Saved "+name+" in "+target, m.subtitle)
					m.chosen = "back"
				}
			default:
				require.Equal(t, "path", m.kind)
				require.Equal(t, "first.csv", name)
				m.input.SetValue(link + "/../first.csv")
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				require.Equal(t, "save", m.chosen)
			}
			return nil
		})
		require.NoError(t, err)
		require.FileExists(t, filepath.Join(target, name))
		require.NoFileExists(t, filepath.Join(root, name))
	}
}

func TestBatchCSVViewerRejectsMissingSourceBeforeShowingRows(t *testing.T) {
	batch := gjson.Parse(`{"status":"completed","input_file_id":"source"}`)
	err := browseBatchCSVResults(t.Context(), openai.NewClient(option.WithAPIKey("synthetic")), nil, batch,
		filepath.Join(t.TempDir(), "missing.csv"), newBatchBrowserActions("batch_csv"), func(_ context.Context, m *batchBrowserScreen) error {
			require.Equal(t, "Reading CSV results", m.title)
			return nil
		})
	var sourceErr *batchCSVSourceError
	require.ErrorAs(t, err, &sourceErr)
	require.ErrorContains(t, err, "original CSV")
}

func TestBatchCSVSaveCanRecoverFromMissingDefaultFolder(t *testing.T) {
	dir := t.TempDir()
	sheet, err := os.CreateTemp(dir, "prepared-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sheet.Close() })
	_, err = sheet.WriteString("ticket,result\n001,billing\n")
	require.NoError(t, err)
	dest := filepath.Join(dir, "accessible.csv")
	bad := filepath.Join(dir, "removed", "tickets-results.csv")
	visits := 0
	saved, err := saveBrowserCSVSheet(t.Context(), sheet, bad, newBatchBrowserActions("batch_csv"), false,
		func(_ context.Context, m *batchBrowserScreen) error {
			visits++
			require.Equal(t, "path", m.kind)
			require.Contains(t, m.notice, "Ctrl+L")
			m.input.SetValue(dest)
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			require.Equal(t, "save", m.chosen)
			return nil
		})
	require.NoError(t, err)
	require.Equal(t, 1, visits)
	require.Equal(t, dest, saved)
	data, err := os.ReadFile(dest)
	require.NoError(t, err)
	require.Equal(t, "ticket,result\n001,billing\n", string(data))
}
