package batches

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
)

type batchBrowserProgress struct {
	ctx      context.Context
	fetch    func(context.Context) (gjson.Result, error)
	cancel   context.CancelFunc
	sequence uint64
	err      error
	paused   bool
}

type batchBrowserProgressTick struct {
	screen   *batchBrowserScreen
	sequence uint64
}
type batchBrowserProgressReady struct {
	screen   *batchBrowserScreen
	sequence uint64
	batch    gjson.Result
	err      error
}

func batchBrowserProcessing(batch gjson.Result) bool {
	switch batch.Get("status").Str {
	case "validating", "in_progress", "finalizing", "cancelling":
		return true
	}
	return false
}

func fetchBatchBrowserDetails(ctx context.Context, client openai.Client, options []option.RequestOption, id string) (gjson.Result, error) {
	var raw []byte
	opts := append(options[:len(options):len(options)], option.WithResponseBodyInto(&raw))
	if _, err := client.Batches.Get(ctx, id, opts...); err != nil {
		return gjson.Result{}, err
	}
	batch := gjson.ParseBytes(raw)
	if !gjson.ValidBytes(raw) || !batch.IsObject() || batch.Get("id").Str != id {
		return gjson.Result{}, batchError("The API returned invalid Batch details. Return to the action menu and retry Batch details.")
	}
	return batch, nil
}

func (m *batchBrowserScreen) stopProgress() {
	if m.progress == nil {
		return
	}
	if m.progress.cancel != nil {
		m.progress.cancel()
		m.progress.cancel = nil
	}
	m.progress.sequence++
}

func (m *batchBrowserScreen) startProgress() tea.Cmd {
	m.stopProgress()
	if m.progress.paused || !batchBrowserProcessing(m.overview) {
		return nil
	}
	sequence := m.progress.sequence
	ctx, cancel := context.WithCancel(m.progress.ctx)
	m.progress.cancel = cancel
	return func() tea.Msg {
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
		return batchBrowserProgressTick{m, sequence}
	}
}

func (m *batchBrowserScreen) updateProgress(message tea.Msg) (tea.Cmd, bool) {
	s := m.progress
	switch msg := message.(type) {
	case batchBrowserProgressTick:
		if s == nil || msg.screen != m || msg.sequence != s.sequence || m.closed {
			return nil, true
		}
		if s.cancel != nil {
			s.cancel()
		}
		ctx, cancel := context.WithCancel(s.ctx)
		s.cancel = cancel
		return func() tea.Msg {
			batch, err := s.fetch(ctx)
			return batchBrowserProgressReady{m, msg.sequence, batch, err}
		}, true
	case batchBrowserProgressReady:
		if s == nil || msg.screen != m || msg.sequence != s.sequence || m.closed {
			return nil, true
		}
		if s.cancel != nil {
			s.cancel()
			s.cancel = nil
		}
		if msg.err != nil {
			s.err, s.paused = msg.err, true
			m.chosen, m.closed = "poll-error", true
			return tea.Quit, true
		}
		m.setOverview(msg.batch)
		return m.startProgress(), true
	}
	return nil, false
}
