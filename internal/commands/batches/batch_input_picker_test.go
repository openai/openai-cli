package batches

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBatchInputPickerSelectionAndCancellation(t *testing.T) {
	items := []gjson.Result{gjson.Parse(`{"id":"file_first","filename":"first.jsonl","future":9007199254740993}`), gjson.Parse(`{"id":"file_second","filename":"second.jsonl"}`)}
	model := newBatchInputPicker(t.Context(), items, 80, 24)
	model.Update(tea.WindowSizeMsg{Width: 20, Height: 4})
	model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, blocked := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, blocked, "a hidden choice must not be selected")
	require.False(t, model.selected.Exists())
	model.Update(tea.WindowSizeMsg{Width: 20, Height: 5})
	_, quit := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, quit)
	require.Equal(t, items[1].Raw, model.selected.Raw)
	require.NoError(t, model.err)
	model = newBatchInputPicker(t.Context(), items, 80, 24)
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, items[0].Raw, model.selected.Raw)
	model = newBatchInputPicker(t.Context(), items, 80, 24)
	model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.False(t, model.selected.Exists())
	var status interface{ ExitCode() int }
	require.ErrorAs(t, model.err, &status)
	require.Equal(t, 130, status.ExitCode())
}

func TestBatchInputPickerEscapesLabels(t *testing.T) {
	item := batchInputItem{value: gjson.Parse(`{"id":"file_\u001b[31m","filename":"資料\n\t\u202e.jsonl"}`)}
	for _, value := range []string{item.Title(), item.Description(), item.FilterValue()} {
		require.False(t, strings.ContainsAny(value, "\x1b\n\t\u202e"), value)
	}
	require.Contains(t, item.Title(), "資料")
	require.Contains(t, item.Title(), `\u202e`)
}

func TestBatchInputPickerFiltersLiteralNamesAndIDs(t *testing.T) {
	items := []gjson.Result{gjson.Parse(`{"id":"file_alpha","filename":"Requests[1].JSONL"}`), gjson.Parse(`{"id":"file_beta","filename":"requests1.jsonl"}`)}
	model := newBatchInputPicker(t.Context(), items, 80, 24)
	model.Update(tea.KeyPressMsg{Code: 'r', Text: "requests[1]"})
	require.Equal(t, []int{0}, model.visible)
	model.input.SetValue("FILE_BETA")
	model.filter()
	require.Equal(t, []int{1}, model.visible)
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, items[1].Raw, model.selected.Raw)
}

type batchPickerFailWriter struct{}

func (batchPickerFailWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestBatchInputPickerStopsOnOutputFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	writer := &batchPickerOutput{TerminalWriter: clioutput.TerminalWriter{Writer: clioutput.Writer{Context: t.Context(), Sink: batchPickerFailWriter{}}}, cancel: cancel}
	_, err := writer.WriteString("synthetic frame")
	require.ErrorIs(t, err, io.ErrClosedPipe)
	require.True(t, errors.Is(writer.err, io.ErrClosedPipe))
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestBatchInputPickerCancellationBypassesBlockedDelivery(t *testing.T) {
	for _, key := range []uv.KeyPressEvent{{Code: 'c', Mod: uv.ModCtrl}, {Code: uv.KeyEscape}} {
		ctx, stop := context.WithCancelCause(t.Context())
		events, deliveries, done := make(chan uv.Event, 4), make(chan uv.Event), make(chan struct{})
		events <- uv.KeyPressEvent{Code: 'a', Text: "a"}
		events <- uv.KeyPressEvent{Code: 'b', Text: "b"}
		events <- key
		close(events)
		go func() { routeBatchPickerInput(ctx, events, deliveries, stop); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("keyboard cancellation waited for blocked delivery")
		}
		var status *WorkflowError
		require.ErrorAs(t, context.Cause(ctx), &status)
		require.Equal(t, 130, status.ExitCode())
		stop(nil)
	}
}

func TestBatchInputPickerBoundsPendingKeyboardInput(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		ctx, stop := context.WithCancelCause(t.Context())
		events, deliveries, done := make(chan uv.Event, 65), make(chan uv.Event), make(chan struct{})
		if oversized {
			events <- uv.PasteEvent{Content: strings.Repeat("x", (1<<20)+1)}
		} else {
			for range 65 {
				events <- uv.KeyPressEvent{Code: 'a', Text: "a"}
			}
		}
		close(events)
		go func() { routeBatchPickerInput(ctx, events, deliveries, stop); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("excess input did not stop the picker")
		}
		var status *WorkflowError
		require.ErrorAs(t, context.Cause(ctx), &status)
		require.Equal(t, 1, status.ExitCode())
		require.Contains(t, status.Error(), "Too much pending keyboard input")
		stop(nil)
	}
}

func TestBatchInputPickerPreservesDecodedPasteAndKeyOrder(t *testing.T) {
	ctx, stop := context.WithCancelCause(t.Context())
	defer stop(nil)
	events, deliveries, done := make(chan uv.Event, 4), make(chan uv.Event), make(chan struct{})
	want := []uv.Event{uv.KeyPressEvent{Code: uv.KeyDown}, uv.PasteEvent{Content: "literal \x03 text"}, uv.KeyPressEvent{Code: uv.KeyEnter}}
	for _, event := range want {
		events <- event
	}
	go func() { routeBatchPickerInput(ctx, events, deliveries, stop); close(done) }()
	for _, event := range want {
		select {
		case got := <-deliveries:
			require.Equal(t, event, got)
		case <-time.After(time.Second):
			t.Fatal("decoded input delivery stopped")
		}
	}
	require.NoError(t, ctx.Err(), "a pasted Ctrl+C must not cancel")
	stop(nil)
	close(events)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("input cleanup did not join")
	}
}

func TestBatchInputPickerFilteringObservesCancellation(t *testing.T) {
	ctx, stop := context.WithCancel(t.Context())
	stop()
	model := newBatchInputPicker(ctx, []gjson.Result{gjson.Parse(`{"id":"file_one"}`)}, 80, 24)
	require.ErrorIs(t, model.err, context.Canceled)
	require.Empty(t, model.visible)
}
