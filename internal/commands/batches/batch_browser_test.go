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
	require.Equal(t, []int{0, 2, 3, 4, 5}, m.visible)
	m.input.SetValue("nothing")
	m.filter()
	require.Equal(t, []int{2, 3, 4, 5}, m.visible, "page controls must survive an empty search")
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "next", m.chosen)
	m.input.SetValue("")
	m.filter()
	require.NotContains(t, m.View().Content, "\x1b[31m")
	require.Contains(t, m.View().Content, "Search covers this page only")
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
		m := newBatchBrowserPath("batch_one", "output")
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

func browserTestCommand(t *testing.T, server string, out io.Writer, show func(context.Context, *batchBrowserScreen) error) *cli.Command {
	root := batchDownloadTestCommand(t, server, out)
	w := workflow{options: testOptions()}
	command := w.newBatchBrowseCommand()
	command.Action = func(ctx context.Context, cmd *cli.Command) error {
		options, err := w.options.RequestOptions(cmd, strings.NewReader(""))
		if err != nil {
			return err
		}
		client := openai.NewClient(w.options.DefaultRequestOptions(cmd)...)
		return w.runBatchBrowser(ctx, cmd, client, options, show)
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
	steps := []browserTestStep{{"list", "batch:batch_one", ""}, {"actions", "status", ""}, {"return", "back", ""}, {"actions", "output", ""}, {"return", "back", ""}, {"actions", "error", ""}, {"return", "back", ""}, {"actions", "input", ""}, {"return", "back", ""}, {"actions", "download", ""}, {"download", "output", ""}, {"path", "save", output}, {"return", "back", ""}, {"actions", "back", ""}, {"list", "quit", ""}}
	var out bytes.Buffer
	root := browserTestCommand(t, server.URL, &out, browserTestUI(t, steps))
	require.NoError(t, root.Run(t.Context(), []string{"openai", "--project", "project_browser", "--header", "X-Browser: fixture", "batches", "browse"}))
	require.Contains(t, out.String(), "positive")
	require.Contains(t, out.String(), "zero failed requests")
	require.NotContains(t, out.String(), "synthetic-batch-key")
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	require.Contains(t, string(data), "positive")
	require.Contains(t, paths, "/files/file_input/content")
	require.False(t, root.IsSet("batch-id"))
	require.False(t, root.IsSet("file"))
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
	steps := []browserTestStep{{"list", "batch:batch_one", ""}, {"actions", "status", ""}, {"return", "back", ""}, {"actions", "back", ""}, {"list", "quit", ""}}
	var out bytes.Buffer
	root := browserTestCommand(t, server.URL, &out, browserTestUI(t, steps))
	err := root.Run(t.Context(), []string{"openai", "batches", "browse"})
	var apierr *openai.Error
	require.ErrorAs(t, err, &apierr)
	require.Equal(t, http.StatusForbidden, apierr.StatusCode)
	require.NotContains(t, out.String(), "synthetic API error")
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
	steps := []browserTestStep{{"list", "batch:batch_one", ""}, {"actions", "download", ""}, {"download", "output", ""}, {"path", "save", path}, {"return", "back", ""}, {"actions", "back", ""}, {"list", "quit", ""}}
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
			require.Len(t, m.visible, 3, "only Next, Refresh, and Quit match on the first page")
			m.chosen = "next"
		case 1:
			require.Equal(t, "two", m.input.Value())
			require.Equal(t, "batch:batch_two", m.choices[m.visible[0]].value)
			m.chosen = "previous"
		case 2:
			require.Len(t, m.visible, 3)
			m.chosen = "refresh"
		case 3:
			m.chosen = "quit"
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
	steps := []browserTestStep{{"list", "batch:batch_one", ""}, {"actions", "download", ""}, {"download", "output", ""}, {"path", "save", path}, {"return", "back", ""}, {"actions", "download", ""}, {"download", "output", ""}, {"path", "back", ""}, {"actions", "back", ""}, {"list", "quit", ""}}
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
	steps := []browserTestStep{{"list", "next", ""}, {"list", "previous", ""}, {"list", "next", ""}, {"list", "next", ""}, {"list", "previous", ""}, {"list", "quit", ""}}
	root := browserTestCommand(t, server.URL, io.Discard, browserTestUI(t, steps))
	require.NoError(t, root.Run(t.Context(), []string{"openai", "batches", "browse"}))
	require.Equal(t, []string{"", "batch_A", "", "batch_X", "batch_Y", "batch_X"}, cursors)
}
