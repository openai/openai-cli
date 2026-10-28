package batches

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestBatchGuidedConfirmationNeedsDistinctKey(t *testing.T) {
	m := newBatchBrowserFlow("confirm", "Review batch", "", nil)
	m.flow.lines = []string{"Input: requests.jsonl", "Endpoint: /v1/responses"}
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

func TestBatchGuidedReviewHidesConnectionSecrets(t *testing.T) {
	root := &cli.Command{Flags: []cli.Flag{&cli.StringFlag{Name: "base-url"}, &cli.StringFlag{Name: "project"}, &cli.StringSliceFlag{Name: "header"}}, Action: func(ctx context.Context, cmd *cli.Command) error {
		draft := newBatchBrowserDraft()
		draft.inputID = "file_synthetic"
		text := strings.Join(batchBrowserReview(cmd, draft, nil), "\n")
		require.Contains(t, text, "https://fixture.invalid")
		require.Contains(t, text, "proj_override")
		for _, secret := range []string{"password", "private-path", "secret-query", "private-key"} {
			require.NotContains(t, text, secret)
		}
		return nil
	}}
	require.NoError(t, root.Run(t.Context(), []string{"openai", "--base-url", "https://name:password@fixture.invalid/private-path?token=secret-query", "--project", "proj_original", "--header", "OpenAI-Project: proj_override", "--header", "Authorization: Bearer private-key"}))
}
