package batches

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestBatchBrowserCSVReimportReplacesUploadedDraft(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tickets.csv")
	require.NoError(t, os.WriteFile(path, []byte("id,message\n001,changed ticket\n"), 0600))
	for _, reimport := range []bool{false, true} {
		draft := newBatchBrowserDraft()
		draft.csv = &batchBrowserCSV{path: path, column: "message", instructions: "Classify.", model: "fake",
			header: []string{"id", "message"}}
		draft.inputID = "file_reviewed"
		completed, notice, err := editBatchBrowserCSV(t.Context(), draft, path, reimport, func(_ context.Context, screen *batchBrowserScreen) error {
			switch screen.title {
			case "Set up CSV":
				screen.chosen = "review"
			case "Reading CSV headings":
			default:
				t.Fatalf("unexpected screen %s", screen.title)
			}
			return nil
		})
		require.NoError(t, err)
		require.True(t, completed)
		require.Empty(t, notice)
		if reimport {
			require.Empty(t, draft.inputID)
			require.Equal(t, path, draft.path)
		} else {
			require.Equal(t, "file_reviewed", draft.inputID)
			require.Empty(t, draft.path)
		}
	}
}

func TestBatchCSVFormKeepsFieldsEditableAndRequiresTaskAndModel(t *testing.T) {
	csv := &batchBrowserCSV{path: "tickets.csv", header: []string{"ticket ID", "message"}}
	m := newBatchBrowserCSVForm(csv)
	key := func(code rune) { m.Update(tea.KeyPressMsg{Code: code}) }
	key(tea.KeyDown)
	key(tea.KeyEnter)
	require.Equal(t, "message", csv.column)
	key(tea.KeyEnter)
	require.Contains(t, m.notice, "instructions")
	m.Update(tea.PasteMsg{Content: "Classify this."})
	key(tea.KeyEnter)
	key(tea.KeyEnter)
	require.Contains(t, m.notice, "model name")
	key(tea.KeyUp)
	require.Equal(t, "Classify this.", m.input.Value())
	key(tea.KeyEnter)
	m.Update(tea.PasteMsg{Content: "synthetic"})
	key(tea.KeyEnter)
	require.Equal(t, "review", m.chosen)
	require.Equal(t, "synthetic", csv.model)
}

func TestBatchCSVJoinHintOnlyForRecognizedRows(t *testing.T) {
	id := batchCSVRowID([]string{"message"}, []string{"hello"}, 1)
	for _, sample := range []struct {
		ids   []string
		offer bool
	}{
		{[]string{id}, true}, {[]string{id, "ticket-1"}, false}, {[]string{"csv-arbitrary"}, false}, {nil, false},
	} {
		var rows []string
		for _, id := range sample.ids {
			rows = append(rows, fmt.Sprintf(`{"custom_id":%q}`, id))
		}
		require.Equal(t, sample.offer, catalogFixture(t, "input", rows...).csvRows)
	}
}

func TestBatchBrowserCSVReviewBackAndInvalidRowsMakeNoRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request before confirmation: %s", r.Method)
		w.WriteHeader(500)
	}))
	defer server.Close()
	client := openai.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("synthetic-preview-key"))
	path := filepath.Join(t.TempDir(), "tickets.csv")
	require.NoError(t, os.WriteFile(path, []byte("id,message\n001,Sample ticket\n"), 0600))
	draft := newBatchBrowserDraft()
	draft.path, draft.csv = path, &batchBrowserCSV{path: path, column: "message", model: "fake",
		instructions: "Classify.", header: []string{"id", "message"}}
	previews := 0
	repaired := false
	command := &cli.Command{Writer: io.Discard, ErrWriter: io.Discard}
	_, err := (workflow{}).createBatchInBrowser(t.Context(), command, client, nil, draft, func(_ context.Context, screen *batchBrowserScreen) error {
		switch screen.title {
		case "Create batch":
			switch {
			case previews == 0:
				screen.chosen = "review"
			case screen.notice != "":
				require.Contains(t, screen.notice, "row 2")
				require.NoError(t, os.WriteFile(path, []byte("id,message\n001,Fixed ticket\n"), 0600))
				screen.chosen, repaired = "review", true
			case previews == 1:
				require.NoError(t, os.WriteFile(path, []byte("id,message\n001,Sample ticket\n002,\n"), 0600))
				screen.chosen = "review"
			default:
				screen.chosen = "back"
			}
		case "Review batch":
			previews++
			view := strings.Join(screen.flow.lines, "\n")
			require.Contains(t, view, "Requests: 1")
			require.Contains(t, view, "Model: fake")
			if previews == 1 {
				require.Contains(t, view, "First input: Sample ticket")
			} else {
				require.Contains(t, view, "First input: Fixed ticket")
			}
			require.NotContains(t, view, "batches submit")
			screen.chosen = "back"
		case "Preparing input file":
		default:
			t.Fatalf("unexpected screen %s", screen.title)
		}
		return nil
	})
	require.NoError(t, err)
	require.True(t, repaired)
	require.Equal(t, 2, previews)
}

func TestBatchBrowserCSVStorageFailureStopsCreation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tickets.csv")
	require.NoError(t, os.WriteFile(path, []byte("message\nhello\n"), 0600))
	// A file cannot serve as a directory, including when tests run as root.
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, path)
	}
	draft := newBatchBrowserDraft()
	draft.path = path
	draft.csv = &batchBrowserCSV{path: path, column: "message", header: []string{"message"}, model: "demo", instructions: "Classify."}
	screens := 0
	_, err := (workflow{}).createBatchInBrowser(t.Context(), &cli.Command{}, openai.Client{}, nil, draft, func(_ context.Context, screen *batchBrowserScreen) error {
		screens++
		switch screens {
		case 1:
			require.Equal(t, "Create batch", screen.title)
			screen.chosen = "review"
		case 2:
			require.Equal(t, "Preparing input file", screen.title)
		default:
			t.Fatalf("storage failure unexpectedly opened %s", screen.title)
		}
		return nil
	})
	require.ErrorContains(t, err, "temporary storage")
	require.Equal(t, 2, screens)
	require.Empty(t, batchBrowserCSVNotice(errors.Join(invalidBatchCSV("Bad cell", nil), err)))
}

func TestBatchBrowserCSVSetupOpensReview(t *testing.T) {
	for _, sample := range []struct {
		name, data string
		columns    int
	}{
		{"single column", "message\nInvoice issue\n", 1},
		{"multiple columns", "id,message\n001,Invoice issue\n", 2},
	} {
		t.Run(sample.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tickets.csv")
			require.NoError(t, os.WriteFile(path, []byte(sample.data), 0600))
			draft := newBatchBrowserDraft()
			cmd := &cli.Command{Writer: io.Discard, ErrWriter: io.Discard}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected request while editing or reviewing: %s", r.Method)
				w.WriteHeader(500)
			}))
			defer server.Close()
			client := openai.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("synthetic-preview-key"))
			var screens []string
			menuVisits := 0
			_, err := (workflow{}).createBatchInBrowser(t.Context(), cmd, client, nil, draft, func(_ context.Context, m *batchBrowserScreen) error {
				screens = append(screens, m.title)
				switch m.title {
				case "Create batch":
					menuVisits++
					if menuVisits == 1 {
						m.input.SetValue(path)
						m.chosen = "input"
					} else {
						m.chosen = "back"
					}
				case "Set up CSV":
					if sample.columns > 1 {
						m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
						m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
					}
					m.Update(tea.PasteMsg{Content: "Classify."})
					m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
					m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
					require.Equal(t, "Classify.", m.input.Value())
					m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
					m.Update(tea.PasteMsg{Content: "synthetic-model"})
					m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
					require.Equal(t, "review", m.chosen)
				case "Review batch":
					require.Contains(t, m.flow.lines, "Input column: message")
					require.Contains(t, m.flow.lines, "First input: Invoice issue")
					m.chosen = "back"
				case "Reading CSV headings", "Preparing input file":
				default:
					t.Fatalf("unexpected screen %s", m.title)
				}
				return nil
			})
			require.NoError(t, err)
			want := []string{"Create batch", "Reading CSV headings", "Set up CSV", "Preparing input file", "Review batch", "Create batch"}
			require.Equal(t, want, screens)
		})
	}
}

func TestBatchBrowserCSVCancelReplacementKeepsDraft(t *testing.T) {
	for _, data := range []string{"text\nhello\n", "id,text\n001,hello\n"} {
		path := filepath.Join(t.TempDir(), "new.csv")
		require.NoError(t, os.WriteFile(path, []byte(data), 0600))
		old := &batchBrowserCSV{path: "old.csv", column: "review", model: "existing-model"}
		draft := newBatchBrowserDraft()
		draft.csv, draft.inputID = old, "file_original"
		visits := 0
		_, err := (workflow{}).createBatchInBrowser(t.Context(), &cli.Command{}, openai.Client{}, nil, draft, func(_ context.Context, m *batchBrowserScreen) error {
			switch m.title {
			case "Create batch":
				visits++
				if visits == 1 {
					m.input.SetValue(path)
					m.chosen = "input"
				} else {
					m.chosen = "back"
				}
			case "Set up CSV":
				m.chosen = "back"
			case "Reading CSV headings":
			default:
				t.Fatalf("unexpected screen after cancelling replacement: %s", m.title)
			}
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, 2, visits)
		require.Same(t, old, draft.csv)
		require.Equal(t, "file_original", draft.inputID)
	}
}

func TestBatchBrowserCSVCompletionPreservesActionFocus(t *testing.T) {
	running := gjson.Parse(`{"status":"in_progress","endpoint":"/v1/responses","input_file_id":"file_1"}`)
	partial := gjson.Parse(`{"status":"completed","endpoint":"/v1/responses","input_file_id":"file_1","error_file_id":"errors","request_counts":{"total":3,"completed":2,"failed":1}}`)
	for _, selected := range []string{"output", "error", "status"} {
		actions := newBatchBrowserActions("batch_1")
		actions.actionsState.csvSource = "tickets.csv"
		actions.setOverview(running)
		for i, choice := range actions.choices {
			if choice.value == selected {
				actions.index = i
			}
		}
		actions.setOverview(partial)
		require.Equal(t, selected, actions.choices[actions.index].value)
		require.Equal(t, "View rows with answers", actions.choices[0].title)
		require.Equal(t, "View errors", actions.choices[1].title)
		actions.setSaveExpanded(true)
		require.Equal(t, "save:output", actions.choices[actions.index].value)
		require.Equal(t, "raw-output", actions.choices[len(actions.choices)-1].value)
	}
	actions := newBatchBrowserActions("batch_other")
	actions.setOverview(partial)
	require.Equal(t, "View results", actions.choices[0].title)
	require.Equal(t, "Save a file", actions.choices[len(actions.choices)-1].title)
}

func TestBatchBrowserCSVReadKeepsTerminalFailure(t *testing.T) {
	boom := errors.New("terminal disconnected")
	_, _, err := editBatchBrowserCSV(t.Context(), newBatchBrowserDraft(), filepath.Join(t.TempDir(), "missing.csv"), true,
		func(context.Context, *batchBrowserScreen) error { return boom })
	require.ErrorIs(t, err, boom)
}

func TestBatchBrowserCSVExportAfterReopening(t *testing.T) {
	header, row := []string{"ticket", "message"}, []string{"001", "How do I sign in?"}
	dir := t.TempDir()
	source, dest := filepath.Join(dir, "tickets.csv"), filepath.Join(dir, "tickets-results.csv")
	require.NoError(t, os.WriteFile(source, []byte("ticket,message\n001,How do I sign in?\n"), 0600))
	wrong := filepath.Join(dir, "different.csv")
	require.NoError(t, os.WriteFile(wrong, []byte("ticket,message\n001,Different text\n"), 0600))
	id := batchCSVRowID(header, row, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("export issued %s", r.Method)
		}
		switch r.URL.Path {
		case "/files/source/content":
			fmt.Fprintf(w, "{\"custom_id\":%q}\n", id)
		case "/files/result/content":
			fmt.Fprintf(w, `{"custom_id":%q,"response":{"status_code":200,"body":{"object":"response","output":[{"type":"message","content":[{"type":"output_text","text":"login"}]}]}}}`, id)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := openai.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("synthetic-reopen-key"))
	batch := gjson.Parse(`{"id":"batch_reopen","status":"completed","endpoint":"/v1/responses","input_file_id":"source","output_file_id":"result"}`)
	actions := newBatchBrowserActions("batch_reopen")
	actions.setOverview(batch)
	visits, picks, lists := 0, 0, 0
	err := (workflow{options: testOptions()}).browseBatchRecords(t.Context(), &cli.Command{}, client, nil, batch, "output", actions, func(_ context.Context, m *batchBrowserScreen) error {
		switch {
		case m.kind == "requests":
			lists++
			require.Equal(t, 1, lists, "source corrections stay in the picker")
			require.True(t, m.requestState.offerCSV)
			m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
		case m.title == "Choose the original CSV":
			picks++
			if picks > 1 {
				require.Contains(t, m.notice, "does not match")
			}
			m.chosen = "path"
		case m.flow != nil && m.flow.mode == "entry":
			if picks == 1 {
				m.input.SetValue(wrong)
			} else {
				m.input.SetValue(source)
			}
			m.chosen = "value"
		case m.title == "Reading CSV results":
		case m.title == "Rows with answers":
			visits++
			if visits == 1 {
				m.chosen = "save"
			} else {
				m.chosen = "back"
			}
		default:
			t.Fatalf("unexpected screen %s", m.title)
		}
		return nil
	}, func(context.Context) error { t.Fatal("no printing expected"); return nil })
	require.NoError(t, err)
	require.Equal(t, 2, picks)
	require.Equal(t, source, actions.actionsState.csvSource)
	saved, err := os.ReadFile(dest)
	require.NoError(t, err)
	require.Contains(t, string(saved), "001,How do I sign in?,login,")
}
