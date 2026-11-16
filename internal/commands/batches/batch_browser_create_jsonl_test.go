package batches

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestBatchBrowserJSONLReviewsTheSelectedFileBeforeUpload(t *testing.T) {
	for _, manual := range []bool{false, true} {
		name, endpoint := "automatic", "/v1/embeddings"
		input := `{"url":"/v1/embeddings"}` + "\n\n" + `{"url":"/v1/embeddings"}` + "\n"
		if manual {
			name, endpoint = "explicit", "/v1/chat/completions"
			input = "{}\n" // An explicit endpoint preserves the previous server-side validation contract.
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "reviews.jsonl")
			require.NoError(t, os.WriteFile(path, []byte(input), 0600))
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/files":
					file, _, err := r.FormFile("file")
					require.NoError(t, err)
					defer file.Close()
					data, err := io.ReadAll(file)
					require.NoError(t, err)
					require.Equal(t, input, string(data))
					_, _ = io.WriteString(w, `{"id":"file_demo","object":"file","purpose":"batch"}`)
				case "/batches":
					data, err := io.ReadAll(r.Body)
					require.NoError(t, err)
					require.Equal(t, endpoint, gjson.GetBytes(data, "endpoint").Str)
					require.Equal(t, "file_demo", gjson.GetBytes(data, "input_file_id").Str)
					_, _ = io.WriteString(w, `{"id":"batch_demo","object":"batch","status":"validating"}`)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			client := openai.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("synthetic-browser-key"))
			visits := 0
			batch, err := (workflow{}).createBatchInBrowser(t.Context(), &cli.Command{}, client, nil, newBatchBrowserDraft(), func(_ context.Context, m *batchBrowserScreen) error {
				switch m.title {
				case "Create batch":
					visits++
					m.Update(tea.WindowSizeMsg{Width: 84, Height: 22})
					view := ansi.Strip(m.View().Content)
					for _, text := range []string{"Create batch", "Options", "One prepared API request per line.", "Turn each spreadsheet row into a request."} {
						require.Contains(t, view, text)
					}
					require.NotContains(t, view, "Review and create")
					if manual && visits == 1 {
						m.chosen = "endpoint"
					} else {
						m.input.SetValue(path)
						m.chosen = "input"
					}
				case "Endpoint":
					m.chosen = endpoint
				case "Review batch":
					require.Empty(t, requests)
					require.Contains(t, m.flow.lines, "Input file: reviews.jsonl")
					require.Contains(t, m.flow.lines, "Endpoint: "+endpoint)
					if !manual {
						require.Contains(t, m.flow.lines, "Requests: 2")
					}
					m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
					require.Equal(t, "confirm", m.chosen)
				case "Preparing input file", "Creating batch":
				default:
					t.Fatalf("unexpected screen %s", m.title)
				}
				return nil
			})
			require.NoError(t, err)
			require.Equal(t, "batch_demo", batch.Get("id").Str)
			require.Equal(t, []string{"/files", "/batches"}, requests)
		})
	}
}

func TestBatchBrowserJSONLCanRepairAndReviewWithoutUploading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mixed.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("{\"url\":\"/v1/responses\"}\n{\"url\":\"/v1/embeddings\"}\n"), 0600))
	var screens []string
	_, err := (workflow{}).createBatchInBrowser(t.Context(), &cli.Command{}, openai.Client{}, nil, newBatchBrowserDraft(), func(_ context.Context, m *batchBrowserScreen) error {
		screens = append(screens, m.title)
		switch m.title {
		case "Create batch":
			switch len(screens) {
			case 1:
				m.input.SetValue(path)
				m.chosen = "input"
			case 3:
				require.Contains(t, m.notice, "line 2 uses a different endpoint")
				require.NoError(t, os.WriteFile(path, []byte("{\"url\":\"/v1/embeddings\"}\n"), 0600))
				m.chosen = "review"
			default:
				require.Equal(t, "review", m.choices[m.visible[m.index]].value)
				m.chosen = "back"
			}
		case "Review batch":
			require.Contains(t, strings.Join(m.flow.lines, "\n"), "Requests: 1")
			require.Contains(t, m.flow.lines, "Endpoint: /v1/embeddings")
			m.chosen = "back"
		case "Preparing input file":
		default:
			t.Fatalf("unexpected %s", m.title)
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{"Create batch", "Preparing input file", "Create batch", "Preparing input file", "Review batch", "Create batch"}, screens)
}
