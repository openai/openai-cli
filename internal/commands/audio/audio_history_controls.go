package audio

import (
	"context"
	"errors"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-cli/internal/audiohistory"
)

const (
	audioHistoryClearedNote             = "History cleared."
	audioHistoryClearFailureNote        = "Could not clear all recordings."
	audioHistoryListFailureNote         = "Some audio history could not be read."
	audioHistoryAutoplayFailureNote     = "Could not save History autoplay."
	audioHistoryAutoplayReadFailureNote = "Could not read History autoplay."
)

type audioHistoryFailures struct {
	readAutoplay, writeAutoplay, clear error
}

func (m *audioWorkbench) actionError() error {
	history := m.historyFailures
	return errors.Join(m.lastErr, history.readAutoplay, history.writeAutoplay, history.clear)
}

func (m *audioWorkbench) history() tea.Cmd {
	return m.work("history", func(ctx context.Context) audioWorkMessage {
		entries, listErr := m.store.List(ctx)
		autoplay, autoplayErr := m.store.Autoplay()
		return audioWorkMessage{
			kind: "history", entries: entries, autoplay: autoplay,
			err:         audioFailureIf(audioHistoryListFailureNote, listErr),
			autoplayErr: audioFailureIf(audioHistoryAutoplayReadFailureNote, autoplayErr),
		}
	})
}

func (m *audioWorkbench) toggleHistoryAutoplay() tea.Cmd {
	enabled := !m.historyAutoplay
	return m.work("history-autoplay", func(ctx context.Context) audioWorkMessage {
		err := ctx.Err()
		if err == nil {
			err = m.store.SetAutoplay(enabled)
		}
		return audioWorkMessage{kind: "history-autoplay", autoplay: enabled, err: audioFailureIf(audioHistoryAutoplayFailureNote, err)}
	})
}

func (m *audioWorkbench) updateClearHistory(key string) tea.Cmd {
	switch key {
	case "y", "Y":
		m.stopPlayback()
		return m.work("clear-history", func(ctx context.Context) audioWorkMessage {
			clearErr := m.store.Clear(ctx)
			entries, listErr := m.store.List(ctx)
			return audioWorkMessage{kind: "clear-history", entries: entries, err: audioFailureIf(audioHistoryClearFailureNote, errors.Join(clearErr, listErr))}
		})
	case "n", "N", "enter":
		m.modal = "history"
	}
	return nil
}

func (m *audioWorkbench) applyHistoryResult(msg audioWorkMessage) {
	m.note = ""
	switch msg.kind {
	case "history-autoplay":
		m.historyFailures.writeAutoplay = msg.err
		if msg.err != nil {
			m.historyFailure(audioHistoryAutoplayFailureNote, msg.err)
		} else {
			m.historyAutoplay = msg.autoplay
			m.historyFailures.readAutoplay = nil
		}
	case "history":
		m.modal, m.choice = "history", 0
		m.historyAutoplay = msg.autoplay
		m.historyFailures.readAutoplay = msg.autoplayErr
		m.entries = visibleHistoryEntries(msg.entries)
		m.historySearch = audioHistorySearch{focused: len(m.entries) == 0}
		if msg.err != nil {
			m.note = audioHistoryListFailureNote
		} else if msg.autoplayErr != nil {
			m.historyFailure(audioHistoryAutoplayReadFailureNote, msg.autoplayErr)
		}
	case "clear-history":
		m.modal = "history"
		m.historyFailures.clear = msg.err
		m.refreshClearedHistory(msg.entries, msg.err == nil)
		if msg.err == nil {
			m.note = audioHistoryClearedNote
		} else {
			m.historyFailure(audioHistoryClearFailureNote, msg.err)
		}
	}
}

func (m *audioWorkbench) historyFailure(note string, failure error) {
	m.note, m.failureNote = note, note
	m.failure = m.failureFeedback("history", failure)
	m.failureOffset = 0
}

func visibleHistoryEntries(entries []audiohistory.Entry) []audiohistory.Entry {
	return slices.DeleteFunc(entries, func(entry audiohistory.Entry) bool { return entry.Settings.Preview })
}

// Reload from disk after either full or partial removal. Pending captures can
// complete during Clear, so a successful result can leave History entries.
func (m *audioWorkbench) refreshClearedHistory(entries []audiohistory.Entry, cleared bool) {
	if entries == nil {
		return // The store could not be listed. The prior result is still unverified.
	}
	m.reconcileClearedHistory(entries)
	var selected string
	now := time.Now()
	current := m.visibleHistory(now)
	if m.choice >= 0 && m.choice < len(current) {
		selected = current[m.choice].ID
	}
	m.entries = visibleHistoryEntries(entries)
	if len(m.entries) == 0 {
		m.historySearch.focused = true
		if cleared {
			m.historySearch.query = ""
		}
	}
	m.filterHistory(m.historySearch.query, now)
	for i, entry := range m.visibleHistory(now) {
		if entry.ID == selected {
			m.choice = i
			return
		}
	}
	m.choice = 0
}

// Restore the earlier result if Clear removed only the current preview.
func (m *audioWorkbench) reconcileClearedHistory(entries []audiohistory.Entry) {
	retained := func(result *audioWorkResult) *audioWorkResult {
		if result != nil && slices.ContainsFunc(entries, func(entry audiohistory.Entry) bool { return entry.ID == result.entry.ID }) {
			return result
		}
		return nil
	}
	m.result, m.priorResult = retained(m.result), retained(m.priorResult)
	if m.result == nil {
		m.result, m.priorResult = m.priorResult, nil
	}
	m.playerNotice = ""
}
