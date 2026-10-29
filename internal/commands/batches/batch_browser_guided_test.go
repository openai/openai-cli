package batches

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestBatchGuidedTypePathOnInputRow(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	filename := "notes 日本語.jsonl"
	require.NoError(t, os.WriteFile(filename, []byte(`{"url":"/v1/embeddings"}`+"\n"), 0600))
	draft := newBatchBrowserDraft()
	draft.inputID = "file_existing"
	step := 0
	key := func(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }
	_, err := (workflow{}).createBatchInBrowser(t.Context(), &cli.Command{}, openai.Client{}, nil, draft,
		func(_ context.Context, m *batchBrowserScreen) error {
			if m.title == "Preparing input file" {
				return nil
			}
			if m.title == "Review batch" {
				require.Equal(t, 2, step, "selecting a file should open Review immediately")
				require.Contains(t, m.flow.lines, "Requests: 1")
				require.Contains(t, m.flow.lines, "Endpoint: /v1/embeddings")
				m.Update(key(tea.KeyEscape))
				return nil
			}
			require.Equal(t, "Create batch", m.title)
			step++
			switch step {
			case 1:
				require.NotNil(t, m.View().Cursor)
				m.Update(tea.PasteMsg{Content: "missing.jsonl"})
				require.False(t, m.closed)
				m.Update(key(tea.KeyEnter))
			case 2:
				require.Contains(t, m.notice, "Check its name")
				require.Equal(t, "missing.jsonl", m.input.Value())
				require.Equal(t, "file_existing", draft.inputID)
				m.Update(key(tea.KeyEscape))
				require.False(t, m.closed)
				require.Empty(t, m.input.Value())
				m.Update(key(tea.KeyDown))
				require.Equal(t, "review", m.choices[m.visible[m.index]].value)
				m.Update(key(tea.KeyDown))
				require.Equal(t, "endpoint", m.choices[m.visible[m.index]].value)
				m.Update(tea.PasteMsg{Content: "ignored"})
				require.Empty(t, m.input.Value())
				m.Update(key(tea.KeyUp))
				m.Update(key(tea.KeyUp))
				for _, r := range "notes " {
					m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
				}
				m.Update(tea.PasteMsg{Content: "日本語.jsonl"})
				require.False(t, m.closed)
				require.Equal(t, filename, m.input.Value())
				require.Contains(t, ansi.Strip(m.View().Content), filename)
				m.Update(key(tea.KeyEnter))
			case 3:
				location, err := filepath.EvalSymlinks(draft.path)
				require.NoError(t, err)
				require.Equal(t, filepath.Join(draft.folder, filename), location)
				require.Empty(t, draft.inputID)
				require.Empty(t, m.input.Value())
				require.Equal(t, "review", m.choices[m.visible[m.index]].value)
				m.Update(key(tea.KeyEscape))
			default:
				t.Fatal("unexpected screen")
			}
			require.True(t, m.closed)
			return nil
		})
	require.NoError(t, err)
	require.Equal(t, 3, step)
}

func TestBatchGuidedInputRowStillOpensChooser(t *testing.T) {
	visited := false
	_, err := (workflow{}).createBatchInBrowser(t.Context(), &cli.Command{}, openai.Client{}, nil, newBatchBrowserDraft(),
		func(_ context.Context, m *batchBrowserScreen) error {
			switch m.title {
			case "Create batch":
				if visited {
					m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
					break
				}
				m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
				m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
				require.Empty(t, m.input.Value())
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			case "Choose input file":
				visited = true
				require.True(t, m.flow.localFiles)
				m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			default:
				t.Fatalf("unexpected screen %s", m.title)
			}
			return nil
		})
	require.NoError(t, err)
	require.True(t, visited)
}

func TestBatchGuidedResetNeedsDistinctKey(t *testing.T) {
	m := newBatchBrowserFlow("confirm", "Start a new draft", "", nil)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for range 10 {
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	}
	require.False(t, m.closed)
	m.Update(tea.WindowSizeMsg{Width: 39, Height: 12})
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.False(t, m.closed)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.True(t, m.closed)
	require.Equal(t, "confirm", m.chosen)
}

func TestBatchGuidedProgressPreservesSelectionAndStops(t *testing.T) {
	ctx := t.Context()
	started, cancelled := make(chan struct{}), make(chan struct{})
	m := newBatchBrowserActions("batch_one")
	m.setOverview(gjson.Parse(`{"id":"batch_one","status":"in_progress","request_counts":{"completed":1,"failed":0,"total":3}}`))
	m.index = 2
	m.progress = &batchBrowserProgress{ctx: ctx, fetch: func(ctx context.Context) (gjson.Result, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return gjson.Result{}, ctx.Err()
	}}
	m.Init()
	sequence := m.progress.sequence
	_, fetch := m.Update(batchBrowserProgressTick{m, sequence})
	done := make(chan tea.Msg, 1)
	go func() { done <- fetch() }()
	<-started
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("progress request did not cancel")
	}
	m.Update(<-done)
	require.Equal(t, "back", m.chosen)
	require.Equal(t, "in_progress", m.overview.Get("status").Str)
	m.Init()
	defer m.stopProgress()
	sequence = m.progress.sequence
	_, command := m.Update(batchBrowserProgressReady{m, sequence, gjson.Parse(`{"id":"batch_one","status":"completed","request_counts":{"completed":3,"failed":0,"total":3},"error_file_id":null,"errors":null}`), nil})
	require.Nil(t, command, "completed batch must stop polling")
	require.Equal(t, "input", m.choices[m.visible[m.index]].value)
	other := newBatchBrowserActions("other")
	m.Update(batchBrowserProgressReady{other, sequence, gjson.Parse(`{"id":"wrong"}`), nil})
	require.Equal(t, "batch_one", m.overview.Get("id").Str)
}

func TestBatchGuidedProgressFailurePauses(t *testing.T) {
	m := newBatchBrowserActions("batch_one")
	m.setOverview(gjson.Parse(`{"status":"validating"}`))
	m.progress = &batchBrowserProgress{ctx: t.Context()}
	m.Init()
	m.Update(batchBrowserProgressReady{m, m.progress.sequence, gjson.Result{}, errors.New("synthetic denied")})
	require.True(t, m.progress.paused)
	require.Equal(t, "poll-error", m.chosen)
	require.Nil(t, m.Init())
	require.Contains(t, ansi.Strip(m.View().Content), "Updates paused")
	require.NotContains(t, ansi.Strip(m.View().Content), "Updates every")
}

func TestBatchGuidedScreensFitTerminal(t *testing.T) {
	for _, size := range [][2]int{{30, 7}, {40, 12}, {50, 12}, {100, 30}} {
		for _, mode := range []string{"entry", "picker", "menu", "confirm", "busy"} {
			t.Run(fmt.Sprint(size)+mode, func(t *testing.T) {
				m := newBatchBrowserFlow(mode, "Create batch", strings.Repeat("/long/folder", 20), []batchBrowserChoice{{value: "file", title: "requests.jsonl", detail: strings.Repeat("Details ", 50)}})
				m.flow.lines = []string{strings.Repeat("review ", 80)}
				m.notice = "Check this file and try again."
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				view := m.View()
				lines := strings.Split(ansi.Strip(view.Content), "\n")
				require.LessOrEqual(t, len(lines), size[1], view.Content)
				for _, line := range lines {
					require.LessOrEqual(t, ansi.StringWidth(line), size[0], line)
				}
				control := "Esc"
				if mode == "busy" {
					control = "Ctrl+C"
				}
				require.Contains(t, ansi.Strip(view.Content), control)
			})
		}
	}
}

func TestBatchGuidedFormPaletteAndCaret(t *testing.T) {
	for _, theme := range []struct{ background, focus, muted string }{
		{"0", "138;168;255", "164;172;194"},
		{"15", "49;89;188", "101;112;135"},
	} {
		t.Run(theme.background, func(t *testing.T) {
			t.Setenv("NO_COLOR", "")
			t.Setenv("FORCE_COLOR", "")
			t.Setenv("COLORFGBG", theme.background)
			m := newBatchBrowserFlow("create", "Create batch", "Completion window: 24 hours\nFolder: /local/tickets", []batchBrowserChoice{
				{value: "input", title: "Input file", setting: "Type a file path"},
				{value: "endpoint", title: "Endpoint", setting: "/v1/responses"},
				{value: "expires", title: "Output expiration", setting: "Server default"},
			})
			m.Update(tea.WindowSizeMsg{Width: 80, Height: 17})
			m.input.Placeholder = "Type a file path"
			view := m.View()
			for _, item := range []struct{ text, color string }{
				{"Input file", theme.focus}, {"Type a file path", theme.muted}, {"Endpoint", theme.muted}, {"Folder:", theme.muted},
			} {
				fg, _ := batchBrowserTestTextStyle(t, view.Content, item.text)
				require.Equal(t, item.color, fg, item.text)
			}
			lines := strings.Split(ansi.Strip(view.Content), "\n")
			require.NotNil(t, view.Cursor)
			require.Equal(t, tea.CursorBar, view.Cursor.Shape)
			row := lines[view.Cursor.Position.Y]
			require.Contains(t, row, "> Input file")
			column := ansi.StringWidth(strings.SplitN(row, "Type a file path", 2)[0])
			require.Equal(t, column, view.Cursor.Position.X)
			require.Equal(t, column, ansi.StringWidth(strings.SplitN(lines[view.Cursor.Position.Y+1], "/v1/responses", 2)[0]))
			m.input.SetValue("日本")
			require.Equal(t, column+4, m.View().Cursor.Position.X)
			for _, mode := range []string{"NO_COLOR", "FORCE_COLOR"} {
				value := "1"
				if mode == "FORCE_COLOR" {
					value = "0"
				}
				t.Setenv(mode, value)
				m.input.SetValue("")
				plain := m.View()
				require.Equal(t, ansi.Strip(view.Content), plain.Content)
				require.Equal(t, view.Cursor, plain.Cursor)
				t.Setenv(mode, "")
			}
		})
	}
}

func TestBatchGuidedLongFolderKeepsLastComponentVisible(t *testing.T) {
	m := newBatchBrowserFlow("picker", "Choose input file",
		"Folder: /"+strings.Repeat("界", 80)+"/leaf\nname", nil)
	m.flow.localFiles = true
	m.input.Prompt = "Search  "
	m.Update(tea.WindowSizeMsg{Width: 50, Height: 14})
	view := ansi.Strip(m.View().Content)
	require.Contains(t, view, "Folder: …")
	require.Contains(t, view, `/leaf\nname`)
	require.Contains(t, view, "No matching files or folders.")
	for _, line := range strings.Split(view, "\n") {
		require.Equal(t, 50, ansi.StringWidth(line), line)
	}
}

func TestBatchGuidedCompactFilePickerShowsLocationAndSelection(t *testing.T) {
	m := newBatchBrowserFlow("picker", "Choose input file", "Folder: /work", []batchBrowserChoice{{value: "file", title: "tickets.csv"}})
	m.flow.localFiles = true
	m.input.Prompt = "Search  "
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 7})
	view := ansi.Strip(m.View().Content)
	for _, text := range []string{"Folder: /work", "Search", "> tickets.csv", "Enter", "Ctrl+L", "Esc"} {
		require.Contains(t, view, text)
	}
	for _, line := range strings.Split(view, "\n") {
		require.LessOrEqual(t, ansi.StringWidth(line), 30)
	}
}

func TestBatchGuidedReviewHidesConnectionSecrets(t *testing.T) {
	root := &cli.Command{Flags: []cli.Flag{&cli.StringFlag{Name: "base-url"}, &cli.StringFlag{Name: "project"}, &cli.StringSliceFlag{Name: "header"}}, Action: func(ctx context.Context, cmd *cli.Command) error {
		draft := newBatchBrowserDraft()
		draft.inputID = "file_synthetic"
		text := strings.Join(batchBrowserReview(cmd, draft, nil, 0), "\n")
		require.Contains(t, text, "https://fixture.invalid")
		require.Contains(t, text, "proj_override")
		for _, secret := range []string{"password", "private-path", "secret-query", "private-key"} {
			require.NotContains(t, text, secret)
		}
		return nil
	}}
	require.NoError(t, root.Run(t.Context(), []string{"openai", "--base-url", "https://name:password@fixture.invalid/private-path?token=secret-query", "--project", "proj_original", "--header", "OpenAI-Project: proj_override", "--header", "Authorization: Bearer private-key"}))
}

func TestBatchGuidedExplicitFileWithoutKnownSuffix(t *testing.T) {
	for _, sample := range []struct{ format, filename, data string }{
		{".csv", "tickets", "id,message\n001,Invoice issue\n"},
		{".jsonl", "requests.txt", `{"url":"/v1/responses"}`},
		{"back", "cancel.data", "ignored"},
	} {
		t.Run(sample.format, func(t *testing.T) {
			t.Chdir(t.TempDir())
			require.NoError(t, os.WriteFile(sample.filename, []byte(sample.data), 0600))
			draft := newBatchBrowserDraft()
			draft.inputID = "file_existing"
			visits := 0
			formatSeen, csvSetup := false, false
			_, err := (workflow{}).createBatchInBrowser(t.Context(), &cli.Command{}, openai.Client{}, nil, draft, func(_ context.Context, m *batchBrowserScreen) error {
				switch m.title {
				case "Create batch":
					visits++
					if visits == 1 {
						m.input.SetValue(sample.filename)
						m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
					} else {
						m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
						if !m.closed { // Canceling the choice preserves the editable path.
							m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
						}
					}
				case "Choose file format":
					formatSeen = true
					require.Equal(t, ".jsonl", m.choices[m.visible[m.index]].value)
					if sample.format == "back" {
						m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
					} else {
						if sample.format == ".csv" {
							m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
						}
						m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
					}
				case "Reading CSV headings":
				case "Preparing input file":
				case "Review batch":
					require.Equal(t, ".jsonl", sample.format)
					m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
				case "Set up CSV":
					csvSetup = true
					m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
				default:
					t.Fatalf("unexpected screen: %s", m.title)
				}
				return nil
			})
			require.NoError(t, err)
			require.True(t, formatSeen)
			require.Equal(t, sample.format == ".csv", csvSetup)
			if sample.format == ".jsonl" {
				require.Equal(t, sample.filename, filepath.Base(draft.path))
				require.Empty(t, draft.inputID)
			} else {
				require.Equal(t, "file_existing", draft.inputID)
			}
		})
	}
}
