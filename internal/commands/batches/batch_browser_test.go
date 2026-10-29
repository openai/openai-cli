package batches

import (
	"bytes"
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
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestBatchBrowseAdmission(t *testing.T) {
	for _, flags := range [][]string{nil, {"--format", "text"}, {"--format", "json"}, {"--format-error", "json"}, {"--quiet"}, {"--debug"}, {"--raw-output"}, {"--transform", "id"}, {"--transform-error", "message"}} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
				Flags: []cli.Flag{&cli.StringFlag{Name: "format"}, &cli.StringFlag{Name: "format-error"}, &cli.StringFlag{Name: "transform"}, &cli.StringFlag{Name: "transform-error"}, &cli.BoolFlag{Name: "quiet"}, &cli.BoolFlag{Name: "debug"}, &cli.BoolFlag{Name: "raw-output"}},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					allowed := len(flags) == 0 || flags[0] == "--format" && flags[1] == "text"
					require.Equal(t, allowed, batchBrowseAllowed(cmd, true, true, true))
					for _, terminals := range [][3]bool{{false, true, true}, {true, false, true}, {true, true, false}} {
						require.False(t, batchBrowseAllowed(cmd, terminals[0], terminals[1], terminals[2]))
					}
					return nil
				}}
			require.NoError(t, root.Run(t.Context(), append([]string{"openai"}, flags...)))
		})
	}
}

func TestBatchBrowsePageValidationAndConnection(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"data":[{"id":"batch_one","future":9007199254740993}],"has_more":true}`, true},
		{`{"data":[],"has_more":false}`, true},
		{`{"data":[],"has_more":true}`, false},
		{`{"data":[{"id":"batch_before"}],"has_more":true}`, false},
		{`{"data":[{"status":"completed"}],"has_more":false}`, false},
		{`{"data":[],"has_more":"false"}`, false},
		{`{`, false},
	} {
		t.Run(tc.body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/batches", r.URL.Path)
				require.Equal(t, "20", r.URL.Query().Get("limit"))
				require.Equal(t, "batch_before", r.URL.Query().Get("after"))
				require.Equal(t, "project_browser", r.Header.Get("OpenAI-Project"))
				require.Equal(t, "private-test", r.Header.Get("X-Browser"))
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client := openai.NewClient(option.WithAPIKey("synthetic-browser-key"), option.WithBaseURL(server.URL))
			page, err := fetchBatchBrowsePage(t.Context(), client, []option.RequestOption{option.WithProject("project_browser"), option.WithHeader("X-Browser", "private-test"), option.WithQuery("limit", "99"), option.WithQuery("after", "wrong")}, "batch_before", 20)
			if !tc.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if len(page.items) > 0 {
				require.Equal(t, "9007199254740993", page.items[0].Get("future").Raw)
				require.Equal(t, "batch_one", page.next)
			}
		})
	}
}

func TestBatchBrowserSearchNavigationAndSafety(t *testing.T) {
	page := batchBrowsePage{items: []gjson.Result{gjson.Parse(`{"id":"batch_one","status":"completed","metadata":{"scenario":"SENTIMENT"}}`), gjson.Parse(`{"id":"batch_\u001b[31m","status":"in_progress"}`)}, next: "batch_two"}
	m := newBatchBrowserList(page, true)
	m.input.SetValue("sentiment")
	m.filter()
	require.Equal(t, []int{0, 2, 3}, m.visible)
	m.input.SetValue("nothing")
	m.filter()
	require.Equal(t, []int{2, 3}, m.visible, "page controls must survive an empty search")
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "next", m.chosen)
	m.Init()
	m.input.SetValue("")
	m.filter()
	require.NotContains(t, m.View().Content, "\x1b[31m")
	require.Contains(t, m.View().Content, "Search  ")
	m.Update(tea.WindowSizeMsg{Width: 29, Height: 7})
	m.chosen = ""
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Empty(t, m.chosen)
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 7})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "batch:batch_one", m.chosen)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Equal(t, "back", m.chosen)
}

func TestBatchBrowserPathRequiresExplicitNewFile(t *testing.T) {
	for _, value := range []string{"", "  ", "-", "folder/", "new file.jsonl"} {
		m := newBatchBrowserPath(newBatchBrowserActions("batch_one"), "output")
		m.input.SetValue(value)
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if value == "new file.jsonl" {
			require.Equal(t, "save", m.chosen)
		} else {
			require.Empty(t, m.chosen)
			require.NotEmpty(t, m.notice)
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		require.Equal(t, value, m.input.Value())
	}
}

type browserTestStep struct{ kind, choice, path string }

func browserTestUI(t *testing.T, steps []browserTestStep) func(context.Context, *batchBrowserScreen) error {
	t.Helper()
	index := 0
	t.Cleanup(func() { require.Equal(t, len(steps), index, "not every browser action ran") })
	return func(_ context.Context, m *batchBrowserScreen) error {
		require.Less(t, index, len(steps), "unexpected browser screen")
		step := steps[index]
		index++
		require.Equal(t, step.kind, m.kind)
		m.chosen = step.choice
		if step.path != "" {
			m.input.SetValue(step.path)
		}
		return nil
	}
}

type batchBrowserBoundaryWriter struct {
	io.Writer
	t      *testing.T
	active *bool
}

func (w batchBrowserBoundaryWriter) Write(data []byte) (int, error) {
	require.False(w.t, *w.active, "result output must follow menu teardown")
	return w.Writer.Write(data)
}

func browserTestCommand(t *testing.T, server string, out io.Writer, show func(context.Context, *batchBrowserScreen) error) *cli.Command {
	active := false
	root := batchDownloadTestCommand(t, server, batchBrowserBoundaryWriter{Writer: out, t: t, active: &active})
	w := workflow{options: testOptions()}
	command := w.newBatchBrowseCommand()
	command.Action = func(ctx context.Context, cmd *cli.Command) error {
		options, err := w.options.RequestOptions(cmd, strings.NewReader(""))
		if err != nil {
			return err
		}
		client := openai.NewClient(w.options.DefaultRequestOptions(cmd)...)
		return w.runBatchBrowser(ctx, cmd, client, options, func(ctx context.Context, screen *batchBrowserScreen) error {
			if screen.kind == "return" {
				require.False(t, active, "print return prompts must follow menu teardown")
			} else {
				active = true
			}
			if screen.kind == "document" {
				require.NotNil(t, screen.document, "document screens must retain complete output")
				// Inspect private content separately from the actual stdout boundary.
				require.NoError(t, screen.document.writeTo(ctx, out))
			}
			return show(ctx, screen)
		}, func(context.Context) error {
			active = false
			return nil
		})
	}
	root.Commands[0].Commands = []*cli.Command{command}
	return root
}

func TestBatchBrowserExecutesActionsAndPreservesConnection(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "Bearer synthetic-batch-key", r.Header.Get("Authorization"))
		require.Equal(t, "project_browser", r.Header.Get("OpenAI-Project"))
		require.Equal(t, "fixture", r.Header.Get("X-Browser"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/batches":
			_, _ = io.WriteString(w, `{"data":[{"id":"batch_one","status":"validating"}],"has_more":false}`)
		case "/batches/batch_one":
			_, _ = io.WriteString(w, `{"id":"batch_one","status":"completed","input_file_id":"file_input","output_file_id":"file_output","error_file_id":null,"request_counts":{"failed":0}}`)
		case "/files/file_output/content":
			_, _ = io.WriteString(w, "{\"custom_id\":\"answer\",\"response\":{\"body\":\"positive\"}}\n")
		case "/files/file_input/content":
			_, _ = io.WriteString(w, "{\"custom_id\":\"answer\",\"body\":{\"input\":\"synthetic\"}}\n")
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	output := filepath.Join(t.TempDir(), "new output.jsonl")
	steps := []browserTestStep{{"list", "batch:batch_one", ""}, {"actions", "status", ""}, {"document", "back", ""}, {"actions", "output", ""}, {"requests", "all", ""}, {"document", "back", ""}, {"requests", "back", ""}, {"actions", "error", ""}, {"document", "back", ""}, {"actions", "input", ""}, {"requests", "all", ""}, {"document", "back", ""}, {"requests", "back", ""}, {"actions", "save:output", ""}, {"path", "save", output}, {"document", "back", ""}, {"actions", "back", ""}, {"list", "back", ""}}
	var out bytes.Buffer
	root := browserTestCommand(t, server.URL, &out, browserTestUI(t, steps))
	require.NoError(t, root.Run(t.Context(), []string{"openai", "--project", "project_browser", "--header", "X-Browser: fixture", "batches", "browse"}))
	require.Contains(t, out.String(), "positive")
	require.Contains(t, out.String(), "Error file unavailable")
	require.NotContains(t, out.String(), "No request errors reported", "incomplete snapshots do not establish success")
	require.NotContains(t, out.String(), "synthetic-batch-key")
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	require.Contains(t, string(data), "positive")
	require.Contains(t, paths, "/files/file_input/content")
	require.False(t, root.IsSet("batch-id"))
	require.False(t, root.IsSet("file"))
}

func TestBatchBrowserOverviewRefreshAndFailurePreserveState(t *testing.T) {
	for _, failure := range []string{"API", "invalid JSON"} {
		t.Run(failure, func(t *testing.T) {
			gets := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "project_browser", r.Header.Get("OpenAI-Project"))
				require.Equal(t, "fixture", r.Header.Get("X-Browser"))
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/batches" {
					_, _ = io.WriteString(w, `{"data":[{"id":"batch_one","status":"validating","request_counts":{"completed":0,"failed":0,"total":3}}],"has_more":false}`)
					return
				}
				require.Equal(t, "/batches/batch_one", r.URL.Path)
				gets++
				switch gets {
				case 1:
					_, _ = io.WriteString(w, `{"id":"batch_one","status":"in_progress","request_counts":{"completed":1,"failed":0,"total":3}}`)
				case 2:
					_, _ = io.WriteString(w, `{"id":"batch_one","status":"completed","request_counts":{"completed":3,"failed":0,"total":3}}`)
				case 3:
					if failure == "API" {
						w.WriteHeader(http.StatusForbidden)
						_, _ = io.WriteString(w, `{"error":{"message":"synthetic denied","type":"permission_error"}}`)
					} else {
						_, _ = io.WriteString(w, `{"id":"wrong_batch","status":"failed"}`)
					}
				default:
					t.Error("unexpected detail retrieval")
				}
			}))
			defer server.Close()
			var out bytes.Buffer
			step := 0
			show := func(_ context.Context, m *batchBrowserScreen) error {
				switch step {
				case 0:
					require.Equal(t, "list", m.kind)
					m.input.SetValue("batch_one")
					m.filter()
					m.chosen = "batch:batch_one"
				case 1:
					require.Equal(t, "actions", m.kind)
					require.Equal(t, "validating", m.actionsState.overview.Get("status").Str)
					require.Equal(t, "0", m.actionsState.overview.Get("request_counts.completed").Raw)
					m.index = 3
					m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
				case 2:
					require.Equal(t, "actions", m.kind)
					require.Equal(t, 3, m.index)
					require.Equal(t, "in_progress", m.actionsState.overview.Get("status").Str)
					require.Equal(t, "1", m.actionsState.overview.Get("request_counts.completed").Raw)
					require.Empty(t, out.String(), "refresh must not print a status dump")
					m.chosen = "error"
				case 3:
					require.Equal(t, "document", m.kind)
					require.Equal(t, "Request errors", m.title)
					require.Equal(t, "batch_one", m.subtitle)
					m.chosen = "back"
				case 4, 6:
					require.Equal(t, "actions", m.kind)
					require.Equal(t, 3, m.index)
					require.Equal(t, "completed", m.actionsState.overview.Get("status").Str)
					require.Equal(t, "3", m.actionsState.overview.Get("request_counts.completed").Raw)
					m.chosen = "refresh"
					if step == 6 {
						m.chosen = "back"
					}
				case 5:
					require.Equal(t, "document", m.kind)
					require.Equal(t, "Action failed", m.title)
					require.Equal(t, "batch_one", m.subtitle)
					m.chosen = "back"
				case 7:
					require.Equal(t, "list", m.kind)
					require.Equal(t, "batch_one", m.input.Value())
					require.Equal(t, 0, m.index)
					require.False(t, m.searchFocused)
					require.Equal(t, "validating", m.choices[0].status, "the loaded list snapshot must remain stable")
					m.chosen = "back"
				default:
					t.Fatal("unexpected screen")
				}
				step++
				return nil
			}
			root := browserTestCommand(t, server.URL, &out, show)
			err := root.Run(t.Context(), []string{"openai", "--project", "project_browser", "--header", "X-Browser: fixture", "batches", "browse"})
			require.Error(t, err, "refresh failure must remain a failure when quitting")
			if failure == "API" {
				var apierr *openai.Error
				require.ErrorAs(t, err, &apierr)
				require.Equal(t, http.StatusForbidden, apierr.StatusCode)
			} else {
				require.ErrorContains(t, err, "invalid Batch details")
			}
			require.Equal(t, 8, step)
			require.Equal(t, 3, gets)
			require.Contains(t, out.String(), "Error file unavailable")
			require.NotContains(t, out.String(), "No request errors reported", "incomplete snapshots do not establish success")
			require.Equal(t, 1, strings.Count(out.String(), "Status:"), "only the unavailable-file notice prints status")
		})
	}
}

func TestBatchBrowserActionErrorSurvivesReturningAndQuitting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/batches" {
			_, _ = io.WriteString(w, `{"data":[{"id":"batch_one"}],"has_more":false}`)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"message":"synthetic denied","type":"permission_error"}}`)
	}))
	defer server.Close()
	steps := []browserTestStep{{"list", "batch:batch_one", ""}, {"actions", "status", ""}, {"document", "back", ""}, {"actions", "back", ""}, {"list", "back", ""}}
	var out bytes.Buffer
	root := browserTestCommand(t, server.URL, &out, browserTestUI(t, steps))
	err := root.Run(t.Context(), []string{"openai", "batches", "browse"})
	var apierr *openai.Error
	require.ErrorAs(t, err, &apierr)
	require.Equal(t, http.StatusForbidden, apierr.StatusCode)
	require.Contains(t, out.String(), "synthetic API error")
	require.Contains(t, out.String(), "Batch: batch_one")
}

func TestBatchBrowserDownloadPreservesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("keep"), 0600))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/batches":
			_, _ = io.WriteString(w, `{"data":[{"id":"batch_one"}],"has_more":false}`)
		case "/batches/batch_one":
			_, _ = io.WriteString(w, `{"id":"batch_one","output_file_id":"file_one"}`)
		default:
			_, _ = io.WriteString(w, "new bytes")
		}
	}))
	defer server.Close()
	steps := []browserTestStep{{"list", "batch:batch_one", ""}, {"actions", "save:output", ""}, {"path", "save", path}, {"document", "back", ""}, {"actions", "back", ""}, {"list", "back", ""}}
	var out bytes.Buffer
	root := browserTestCommand(t, server.URL, &out, browserTestUI(t, steps))
	err := root.Run(t.Context(), []string{"openai", "batches", "browse"})
	require.Error(t, err)
	require.True(t, errors.Is(err, os.ErrExist))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "keep", string(data))
}

func TestBatchBrowserPageNavigationKeepsSearchLocal(t *testing.T) {
	var cursors []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/batches", r.URL.Path)
		after := r.URL.Query().Get("after")
		cursors = append(cursors, after)
		w.Header().Set("Content-Type", "application/json")
		if after == "batch_one" {
			_, _ = io.WriteString(w, `{"data":[{"id":"batch_two"}],"has_more":false}`)
		} else {
			_, _ = io.WriteString(w, `{"data":[{"id":"batch_one"}],"has_more":true}`)
		}
	}))
	defer server.Close()
	step := 0
	show := func(_ context.Context, m *batchBrowserScreen) error {
		require.Equal(t, "list", m.kind)
		switch step {
		case 0:
			m.input.SetValue("two")
			m.filter()
			require.Len(t, m.visible, 1, "only Next remains on the first page")
			m.chosen = "next"
		case 1:
			require.Equal(t, "two", m.input.Value())
			require.Equal(t, "batch:batch_two", m.choices[m.visible[0]].value)
			m.chosen = "previous"
		case 2:
			require.Len(t, m.visible, 1, "Previous navigation restores the first page's Next control")
			m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
			require.Equal(t, "refresh", m.chosen)
		case 3:
			m.chosen = "back"
		default:
			t.Fatal("unexpected additional page")
		}
		step++
		return nil
	}
	root := browserTestCommand(t, server.URL, io.Discard, show)
	require.NoError(t, root.Run(t.Context(), []string{"openai", "batches", "browse"}))
	require.Equal(t, []string{"", "batch_one", "", ""}, cursors)
	require.Equal(t, 4, step)
}

func TestBatchBrowserRetainsDownloadDraftAfterFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("keep"), 0600))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/batches":
			_, _ = io.WriteString(w, `{"data":[{"id":"batch_one"}],"has_more":false}`)
		case "/batches/batch_one":
			_, _ = io.WriteString(w, `{"id":"batch_one","output_file_id":"file_one"}`)
		default:
			_, _ = io.WriteString(w, "replacement")
		}
	}))
	defer server.Close()
	steps := []browserTestStep{{"list", "batch:batch_one", ""}, {"actions", "save:output", ""}, {"path", "save", path}, {"document", "back", ""}, {"actions", "save:output", ""}, {"path", "back", ""}, {"actions", "back", ""}, {"list", "back", ""}}
	show := browserTestUI(t, steps)
	paths := 0
	root := browserTestCommand(t, server.URL, io.Discard, func(ctx context.Context, m *batchBrowserScreen) error {
		if m.kind == "path" {
			paths++
			if paths == 2 {
				require.Equal(t, path, m.input.Value(), "failed destination must remain editable")
			}
		}
		return show(ctx, m)
	})
	require.Error(t, root.Run(t.Context(), []string{"openai", "batches", "browse"}))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "keep", string(data))
}

func TestBatchBrowserClearedPathMovesWithoutDownload(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyUp}, {Code: tea.KeyDown}} {
		t.Run(key.String(), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/batches":
					_, _ = io.WriteString(w, `{"data":[{"id":"batch_one"}],"has_more":false}`)
				case "/batches/batch_one":
					_, _ = io.WriteString(w, `{"id":"batch_one","output_file_id":"file_one"}`)
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			step := 0
			show := func(_ context.Context, m *batchBrowserScreen) error {
				switch step {
				case 0:
					m.chosen = "batch:batch_one"
				case 1:
					m.setSaveExpanded(true)
					m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				case 2:
					require.Equal(t, "path", m.kind)
					require.Equal(t, "batch_one-output.jsonl", m.input.Value())
					m.input.SetValue("")
					m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
					m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
					require.Empty(t, m.input.Value())
					m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
					require.NotEmpty(t, m.notice)
					m.Update(key)
				case 3:
					require.Equal(t, "actions", m.kind)
					selected := "save:error"
					if key.Code == tea.KeyUp {
						selected = "download"
					}
					require.Equal(t, selected, m.choices[m.visible[m.index]].value)
					require.Empty(t, m.notice)
					m.chosen = "back"
				case 4:
					m.chosen = "back"
				default:
					t.Fatalf("unexpected screen: %s", m.kind)
				}
				step++
				return nil
			}
			root := browserTestCommand(t, server.URL, io.Discard, show)
			require.NoError(t, root.Run(t.Context(), []string{"openai", "batches", "browse"}))
			require.Equal(t, 5, step)
			require.Equal(t, 2, requests)
		})
	}
}

func TestBatchBrowserInputPolicyLeavesEscapeForNavigation(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	events, deliveries := make(chan uv.Event, 1), make(chan uv.Event)
	events <- uv.KeyPressEvent{Code: uv.KeyEscape}
	done := make(chan struct{})
	go func() {
		routeBatchPickerInputWithPolicy(ctx, events, deliveries, cancel, batchPickerInputPolicy{cancelled: "Browser interrupted", ended: "Input ended"})
		close(done)
	}()
	select {
	case event := <-deliveries:
		require.Equal(t, uv.KeyPressEvent{Code: uv.KeyEscape}, event)
	case <-time.After(time.Second):
		t.Fatal("Escape did not reach browser navigation")
	}
	require.NoError(t, ctx.Err())
	cancel(nil)
	close(events)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("browser input did not stop")
	}
}

func TestBatchPickerFilteringUsesOutputCancellation(t *testing.T) {
	m := newBatchInputPicker(t.Context(), []gjson.Result{gjson.Parse(`{"id":"file_one"}`)}, 80, 24)
	ctx, cancel := context.WithCancel(t.Context())
	m.setPickerContext(ctx)
	cancel()
	m.filter()
	require.ErrorIs(t, m.err, context.Canceled)
}

func TestBatchBrowserSearchDecodesUnicodeMetadata(t *testing.T) {
	batch := gjson.Parse(`{"id":"batch_unicode","status":"completed","endpoint":"/v1/responses","metadata":{"scen\u0061rio":"Caf\u00e9"}}`)
	m := newBatchBrowserList(batchBrowsePage{items: []gjson.Result{batch}}, false)
	for _, query := range []string{"Café", "SCENARIO", "/v1/responses"} {
		m.input.SetValue(query)
		m.filter()
		require.Equal(t, "batch:batch_unicode", m.choices[m.visible[0]].value)
	}
}

func TestBatchBrowserPreviousUsesChangedForwardCursor(t *testing.T) {
	var cursors []string
	first := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		after := r.URL.Query().Get("after")
		cursors = append(cursors, after)
		id, more := "batch_A", true
		switch after {
		case "":
			if !first {
				id = "batch_X"
			}
			first = false
		case "batch_A":
			id = "batch_B"
		case "batch_X":
			id = "batch_Y"
		case "batch_Y":
			id, more = "batch_Z", false
		default:
			t.Errorf("unexpected cursor %q", after)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":[{"id":%q}],"has_more":%t}`, id, more)
	}))
	defer server.Close()
	steps := []browserTestStep{{"list", "next", ""}, {"list", "previous", ""}, {"list", "next", ""}, {"list", "next", ""}, {"list", "previous", ""}, {"list", "back", ""}}
	root := browserTestCommand(t, server.URL, io.Discard, browserTestUI(t, steps))
	require.NoError(t, root.Run(t.Context(), []string{"openai", "batches", "browse"}))
	require.Equal(t, []string{"", "batch_A", "", "batch_X", "batch_Y", "batch_X"}, cursors)
}

func TestBatchBrowserValidationDiagnosticsAndErrorFile(t *testing.T) {
	for _, mode := range []string{"validation only", "both", "file denied", "duplicate errors"} {
		t.Run(mode, func(t *testing.T) {
			hasFile := mode == "both" || mode == "file denied"
			fileRequests, batchRequests := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/batches":
					_, _ = io.WriteString(w, `{"data":[{"id":"batch_one","status":"failed"}],"has_more":false}`)
				case "/batches/batch_one":
					batchRequests++
					file := "null"
					if hasFile {
						file = `"file_error"`
					}
					extra := ""
					if mode == "duplicate errors" {
						extra = `"errors":null,"errors":{"data":[]},"errors":{"data":[{"message":"Earlier synthetic diagnostic"}]},`
					}
					_, _ = fmt.Fprintf(w, `{"id":"batch_one","object":"batch","status":"failed","created_at":123,"metadata":{"unrelated":"do not print"},"error_file_id":%s,%s"errors":{"object":"list","data":[{"code":"invalid_json_line","line":9007199254740993,"message":"Synthetic validation failure"}]}}`, file, extra)
				case "/files/file_error/content":
					fileRequests++
					if mode == "file denied" {
						w.WriteHeader(http.StatusForbidden)
						_, _ = io.WriteString(w, `{"error":{"message":"synthetic denied","type":"permission_error"}}`)
						return
					}
					_, _ = io.WriteString(w, "{\"custom_id\":\"synthetic_request\",\"error\":{\"code\":\"server_error\",\"message\":\"Synthetic request failure\"}}\n")
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
				}
			}))
			defer server.Close()
			steps := []browserTestStep{{"list", "batch:batch_one", ""}, {"actions", "error", ""}}
			if mode == "both" {
				steps = append(steps, browserTestStep{"requests", "all", ""}, browserTestStep{"document", "back", ""}, browserTestStep{"requests", "back", ""})
			}
			steps = append(steps, browserTestStep{"document", "back", ""}, browserTestStep{"actions", "back", ""}, browserTestStep{"list", "back", ""})
			show := browserTestUI(t, steps)
			var out bytes.Buffer
			root := browserTestCommand(t, server.URL, &out, func(ctx context.Context, m *batchBrowserScreen) error {
				if m.kind == "document" && !strings.HasSuffix(m.title, " / All records") {
					title := "Request errors"
					if mode == "file denied" {
						title = "Action failed"
					}
					require.Equal(t, title, m.title)
					require.Equal(t, "batch_one", m.subtitle)
				}
				return show(ctx, m)
			})
			err := root.Run(t.Context(), []string{"openai", "batches", "browse"})
			if mode == "file denied" {
				var apierr *openai.Error
				require.ErrorAs(t, err, &apierr)
				require.Equal(t, http.StatusForbidden, apierr.StatusCode)
			} else {
				require.NoError(t, err)
			}
			for _, text := range []string{"Batch validation diagnostics", "Batch: batch_one", "Status: Failed", "Synthetic validation failure", "9007199254740993"} {
				require.Contains(t, out.String(), text)
			}
			for _, text := range []string{"No error file", "created_at", "metadata", "do not print"} {
				require.NotContains(t, out.String(), text)
			}
			if mode == "duplicate errors" {
				require.Contains(t, out.String(), "Earlier synthetic diagnostic")
			}
			if !hasFile {
				require.Zero(t, fileRequests)
				require.Equal(t, 1, batchRequests)
			} else {
				require.Equal(t, 1, fileRequests)
				require.Equal(t, 1, batchRequests, "record browsing reuses the fresh batch details")
				if mode == "both" {
					require.Contains(t, out.String(), "Synthetic request failure")
				}
			}
		})
	}
}

func TestBatchBrowserRefreshRetainsSelectedActionWhenErrorsChange(t *testing.T) {
	gets := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/batches" {
			_, _ = io.WriteString(w, `{"data":[{"id":"batch_one","status":"in_progress"}],"has_more":false}`)
			return
		}
		require.Equal(t, "/batches/batch_one", r.URL.Path)
		gets++
		if gets == 1 {
			_, _ = io.WriteString(w, `{"id":"batch_one","status":"completed","request_counts":{"completed":3,"failed":0,"total":3},"error_file_id":null,"errors":null}`)
		} else {
			_, _ = io.WriteString(w, `{"id":"batch_one","status":"completed","request_counts":{"completed":3,"failed":0,"total":3},"error_file_id":null}`)
		}
	}))
	defer server.Close()
	step := 0
	show := func(_ context.Context, m *batchBrowserScreen) error {
		switch step {
		case 0:
			m.chosen = "batch:batch_one"
		case 1:
			require.Equal(t, "actions", m.kind)
			m.index = 4
			require.Equal(t, "download", m.choices[m.index].value)
			m.chosen = "refresh"
		case 2:
			require.Equal(t, "actions", m.kind)
			require.Len(t, m.choices, 4)
			require.Equal(t, 3, m.index)
			require.Equal(t, "download", m.choices[m.index].value)
			m.chosen = "refresh"
		case 3:
			require.Equal(t, "actions", m.kind)
			require.Len(t, m.choices, 5)
			require.Equal(t, 4, m.index)
			require.Equal(t, "download", m.choices[m.index].value)
			m.chosen = "back"
		case 4:
			m.chosen = "back"
		default:
			t.Fatal("unexpected screen")
		}
		step++
		return nil
	}
	var out bytes.Buffer
	root := browserTestCommand(t, server.URL, &out, show)
	require.NoError(t, root.Run(t.Context(), []string{"openai", "batches", "browse"}))
	require.Equal(t, 5, step)
	require.Equal(t, 2, gets)
	require.Empty(t, out.String())
}

func TestBatchBrowserPrintIsExplicitAndRestoresDocument(t *testing.T) {
	gets := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/batches":
			_, _ = io.WriteString(w, `{"data":[{"id":"batch_one"}],"has_more":false}`)
		case "/batches/batch_one":
			gets++
			_, _ = io.WriteString(w, `{"id":"batch_one","status":"completed","metadata":{"scenario":"complete synthetic details"}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	var out, expected bytes.Buffer
	active, teardown, step := false, 0, 0
	root := batchDownloadTestCommand(t, server.URL, batchBrowserBoundaryWriter{Writer: &out, t: t, active: &active})
	w := workflow{options: testOptions()}
	command := w.newBatchBrowseCommand()
	var original *batchBrowserDocument
	show := func(ctx context.Context, screen *batchBrowserScreen) error {
		switch step {
		case 0:
			active = true
			require.Equal(t, "list", screen.kind)
			screen.chosen = "batch:batch_one"
		case 1:
			require.Equal(t, "actions", screen.kind)
			screen.chosen = "status"
		case 2:
			require.Equal(t, "document", screen.kind)
			require.Equal(t, "Batch details", screen.title)
			require.Empty(t, screen.subtitle, "the printable Batch details already contains the complete ID")
			require.Empty(t, out.String(), "viewing must not dump output into terminal history")
			require.Zero(t, teardown, "viewing must preserve the active panel")
			original = screen.document
			require.NoError(t, original.writeTo(ctx, &expected))
			require.Equal(t, 1, strings.Count(expected.String(), "batch_one"))
			screen.chosen = "print"
		case 3:
			require.Equal(t, "return", screen.kind)
			require.False(t, active)
			require.Equal(t, 1, teardown)
			require.Equal(t, expected.String(), out.String(), "print must preserve every document byte")
			require.Contains(t, out.String(), "complete synthetic details")
			screen.chosen = "back"
		case 4:
			active = true
			require.Equal(t, "document", screen.kind)
			require.Same(t, original, screen.document, "return must reopen the same private document")
			screen.chosen = "back"
		case 5:
			require.Equal(t, "actions", screen.kind)
			screen.chosen = "back"
		case 6:
			require.Equal(t, "list", screen.kind)
			screen.chosen = "back"
		default:
			t.Fatal("unexpected screen after print")
		}
		step++
		return nil
	}
	command.Action = func(ctx context.Context, cmd *cli.Command) error {
		client := openai.NewClient(w.options.DefaultRequestOptions(cmd)...)
		return w.runBatchBrowser(ctx, cmd, client, nil, show, func(context.Context) error {
			teardown++
			active = false
			return nil
		})
	}
	root.Commands[0].Commands = []*cli.Command{command}
	require.NoError(t, root.Run(t.Context(), []string{"openai", "batches", "browse"}))
	require.Equal(t, 7, step)
	require.Equal(t, 1, gets, "printing must not repeat the API request")
	require.ErrorIs(t, original.writeTo(t.Context(), io.Discard), io.ErrClosedPipe, "leaving the action must close private storage")
}

func TestBatchBrowserDocumentPrintFailureStopsNavigation(t *testing.T) {
	for _, phase := range []string{"teardown", "output"} {
		t.Run(phase, func(t *testing.T) {
			document, err := newBatchBrowserDocument(t.Context())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, document.close()) })
			_, err = io.WriteString(document, "Complete synthetic document.\n")
			require.NoError(t, err)
			output := io.Writer(io.Discard)
			if phase == "output" {
				output = batchDownloadFailWriter{}
			}
			cmd := &cli.Command{Name: "browse", Writer: output}
			shown, teardown := 0, 0
			err = showBatchBrowserDocument(t.Context(), cmd, document, "Batch details", "batch_one", func(_ context.Context, screen *batchBrowserScreen) error {
				shown++
				require.Equal(t, 1, shown, "print failure must not reopen a document or return prompt")
				require.Equal(t, "document", screen.kind)
				screen.chosen = "print"
				return nil
			}, func(context.Context) error {
				teardown++
				if phase == "teardown" {
					return io.ErrClosedPipe
				}
				return nil
			})
			require.ErrorIs(t, err, io.ErrClosedPipe)
			require.Equal(t, 1, shown)
			require.Equal(t, 1, teardown)
		})
	}
}

func TestBatchBrowserPrintOutputFailureDoesNotOpenAnotherScreen(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/batches" {
			_, _ = io.WriteString(w, `{"data":[{"id":"batch_one"}],"has_more":false}`)
			return
		}
		require.Equal(t, "/batches/batch_one", r.URL.Path)
		_, _ = io.WriteString(w, `{"id":"batch_one","status":"completed"}`)
	}))
	defer server.Close()
	root := batchDownloadTestCommand(t, server.URL, batchDownloadFailWriter{})
	w := workflow{options: testOptions()}
	command := w.newBatchBrowseCommand()
	var screens []string
	command.Action = func(ctx context.Context, cmd *cli.Command) error {
		client := openai.NewClient(w.options.DefaultRequestOptions(cmd)...)
		return w.runBatchBrowser(ctx, cmd, client, nil, func(_ context.Context, screen *batchBrowserScreen) error {
			screens = append(screens, screen.kind)
			switch len(screens) {
			case 1:
				require.Equal(t, "list", screen.kind)
				screen.chosen = "batch:batch_one"
			case 2:
				require.Equal(t, "actions", screen.kind)
				screen.chosen = "status"
			case 3:
				require.Equal(t, "document", screen.kind)
				screen.chosen = "print"
			default:
				return errors.New("unexpected screen after output failure")
			}
			return nil
		}, func(context.Context) error { return nil })
	}
	root.Commands[0].Commands = []*cli.Command{command}
	err := root.Run(t.Context(), []string{"openai", "batches", "browse"})
	require.ErrorIs(t, err, io.ErrClosedPipe)
	require.Equal(t, []string{"list", "actions", "document"}, screens, "a broken output sink cannot display another error pane")
}
