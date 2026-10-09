package custom

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/openai/openai-cli/internal/tokenizer"
	"github.com/stretchr/testify/require"
)

type tokenizerTestInput struct {
	*io.PipeReader
	closes atomic.Int32
}

func (r *tokenizerTestInput) Cancel() bool {
	_ = r.PipeReader.CloseWithError(context.Canceled)
	return true
}

func (r *tokenizerTestInput) Close() error {
	r.closes.Add(1)
	return r.PipeReader.Close()
}

func collectTokenizerInput(t *testing.T, chunks []string) []tea.Msg {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, writer := io.Pipe()
	bridge, err := startTokenizerInputBridge(ctx, &tokenizerTestInput{PipeReader: raw})
	require.NoError(t, err)
	defer bridge.Close()
	events := make(chan uv.Event)
	decoded := make(chan error, 1)
	go func() {
		defer close(events)
		decoded <- uv.NewTerminalReader(bridge.Input(), "xterm-256color").StreamEvents(ctx, events)
	}()
	written := make(chan error, 1)
	go func() {
		defer writer.Close()
		for _, chunk := range chunks {
			if _, err := io.WriteString(writer, chunk); err != nil {
				written <- err
				return
			}
		}
		written <- nil
	}()
	var result []tea.Msg
	for event := range events {
		// Bubble Tea performs this translation before invoking model.Update.
		var translated tea.Msg = event
		switch event := event.(type) {
		case uv.PasteEvent:
			translated = tea.PasteMsg(event)
		case uv.KeyPressEvent:
			translated = tea.KeyPressMsg(event)
		}
		message := bridge.Resolve(translated)
		switch message.(type) {
		case tea.PasteMsg, tea.KeyPressMsg, tokenizerInputPasteErrorMsg:
			result = append(result, message)
		}
	}
	require.NoError(t, ctx.Err(), "the bridge and decoder must finish without detached reads")
	require.NoError(t, <-decoded)
	require.NoError(t, <-written)
	require.ErrorIs(t, bridge.Close(), io.EOF)
	return result
}

func TestTokenizerInputPreservesExactBracketedPaste(t *testing.T) {
	for name, source := range map[string]string{
		"replacement":         "a\ufffdb",
		"sgr":                 "a\x1b[31mb",
		"invalid":             "a\xffb",
		"controls":            "a\r\n\x00\t\x03b",
		"nested opener":       "a\x1b[200~b",
		"false terminator":    "a\x1b[201xb\x1b\x1b[20c",
		"Unicode":             "\ufeff日本語 e\u0301 👩‍💻\n",
		"literal placeholder": tokenizerPastePlaceholder,
		"empty":               "",
	} {
		t.Run(name, func(t *testing.T) {
			messages := collectTokenizerInput(t, []string{tokenizerPasteStart + source + tokenizerPasteEnd})
			require.Len(t, messages, 1)
			require.Equal(t, tea.PasteMsg{Content: source}, messages[0])
		})
	}
}

func TestTokenizerInputPreservesEveryDelimiterSplit(t *testing.T) {
	wire := tokenizerPasteStart + "x\r\n\ufffd\x1b[31m" + tokenizerPasteEnd
	for split := 1; split < len(wire); split++ {
		messages := collectTokenizerInput(t, []string{wire[:split], wire[split:]})
		require.Equal(t, []tea.Msg{tea.PasteMsg{Content: "x\r\n\ufffd\x1b[31m"}}, messages)
	}
	chunks := make([]string, len(wire))
	for i := 0; i < len(wire); i++ {
		chunks[i] = wire[i : i+1]
	}
	require.Equal(t, []tea.Msg{tea.PasteMsg{Content: "x\r\n\ufffd\x1b[31m"}}, collectTokenizerInput(t, chunks))
}

func TestTokenizerInputExpiredEscapeCannotOpenPaste(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	raw, writer := io.Pipe()
	defer writer.Close()
	bridge, err := startTokenizerInputBridge(ctx, &tokenizerTestInput{PipeReader: raw})
	require.NoError(t, err)
	defer bridge.Close()
	events := make(chan uv.Event)
	decoded := make(chan error, 1)
	go func() {
		defer close(events)
		decoded <- uv.NewTerminalReader(bridge.Input(), "xterm-256color").StreamEvents(ctx, events)
	}()
	escape := make(chan struct{})
	written := make(chan error, 1)
	go func() {
		defer writer.Close()
		if _, err := io.WriteString(writer, "\x1b"); err != nil {
			written <- err
			return
		}
		// Wait for the actual terminal decoder, not an independently timed sleep.
		select {
		case <-ctx.Done():
			written <- ctx.Err()
			return
		case <-escape:
		}
		_, err := io.WriteString(writer, "[200~typed\x03")
		written <- err
	}()
	var keys []tea.KeyPressMsg
	for event := range events {
		var message tea.Msg = event
		switch event := event.(type) {
		case uv.KeyPressEvent:
			message = tea.KeyPressMsg(event)
		case uv.PasteEvent:
			message = tea.PasteMsg(event)
		}
		if key, ok := bridge.Resolve(message).(tea.KeyPressMsg); ok {
			keys = append(keys, key)
			if key.String() == "esc" && len(keys) == 1 {
				close(escape)
			}
		}
	}
	require.NoError(t, ctx.Err(), "a consumed Escape must not retain an opener prefix or wait for a paste acknowledgment")
	require.NoError(t, <-decoded)
	require.NoError(t, <-written)
	require.NotEmpty(t, keys)
	require.Equal(t, "esc", keys[0].String())
	require.Equal(t, "ctrl+c", keys[len(keys)-1].String())
	var text strings.Builder
	for _, key := range keys {
		text.WriteString(key.Text)
	}
	require.Equal(t, "[200~typed", text.String())
	require.ErrorIs(t, bridge.Close(), io.EOF)
}

func TestTokenizerInputExpiredPrefixesMatchTerminalReader(t *testing.T) {
	for end := 1; end < len(tokenizerPasteStart); end++ {
		prefix := tokenizerPasteStart[:end]
		events := make(chan uv.Event, 4)
		err := uv.NewTerminalReader(strings.NewReader(prefix), "xterm-256color").StreamEvents(t.Context(), events)
		require.NoError(t, err)
		close(events)
		var want []tea.Msg
		for event := range events {
			if key, ok := event.(uv.KeyPressEvent); ok {
				want = append(want, tea.KeyPressMsg(key))
			} else {
				want = append(want, event)
			}
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		raw, writer := io.Pipe()
		bridge, err := startTokenizerInputBridge(ctx, &tokenizerTestInput{PipeReader: raw})
		require.NoError(t, err)
		events = make(chan uv.Event)
		decoded := make(chan error, 1)
		go func() {
			defer close(events)
			decoded <- uv.NewTerminalReader(bridge.Input(), "xterm-256color").StreamEvents(ctx, events)
		}()
		written := make(chan error, 1)
		go func() {
			_, err := io.WriteString(writer, prefix)
			written <- errors.Join(err, writer.Close())
		}()
		var got []tea.Msg
		for event := range events {
			var message tea.Msg = event
			switch event := event.(type) {
			case uv.PasteEvent:
				message = tea.PasteMsg(event)
			case uv.KeyPressEvent:
				message = tea.KeyPressMsg(event)
			}
			message = bridge.Resolve(message)
			switch message.(type) {
			case tea.KeyPressMsg, uv.UnknownEvent:
				got = append(got, message)
			}
		}
		require.NoError(t, ctx.Err())
		require.NoError(t, <-decoded)
		require.NoError(t, <-written)
		require.Equal(t, want, got, "expired prefix %q", prefix)
		require.ErrorIs(t, bridge.Close(), io.EOF)
		cancel()
	}
}

func TestTokenizerInputKeepsEscapeKeySequences(t *testing.T) {
	for _, input := range []string{"\x1bx", "\x1b[A", "\x1b\x1b", "\x1b\x1bx", "\x1b\x1b[A", "\x1b\x1bOA",
		"\x1b]11;rgb:0/0/0\x1b", "\x1b]11;unfinished", "\x1b[123", "\x1b[1;5D"} {
		events := make(chan uv.Event, 4)
		require.NoError(t, uv.NewTerminalReader(strings.NewReader(input), "xterm-256color").StreamEvents(t.Context(), events))
		close(events)
		var want []tea.Msg
		for event := range events {
			if key, ok := event.(uv.KeyPressEvent); ok {
				want = append(want, tea.KeyPressMsg(key))
			}
		}
		require.Equal(t, want, collectTokenizerInput(t, []string{input}), "key bytes %q", input)
	}
}

func TestTokenizerInputAdjacentEscapeThenPaste(t *testing.T) {
	for _, prefix := range []string{"\x1b", "\x1b\x1b"} {
		content := "a\r\n\ufffd\x03"
		messages := collectTokenizerInput(t, []string{prefix + tokenizerPasteStart + content + tokenizerPasteEnd + "\x03"})
		require.Len(t, messages, 3)
		want := "esc"
		if len(prefix) == 2 {
			want = "alt+esc"
		}
		require.Equal(t, want, messages[0].(tea.KeyPressMsg).String())
		require.Equal(t, tea.PasteMsg{Content: content}, messages[1])
		require.Equal(t, "ctrl+c", messages[2].(tea.KeyPressMsg).String())
	}
}

func TestTokenizerInputIncompleteSequenceBudget(t *testing.T) {
	for _, size := range []int{tokenizer.MaxInputBytes - 1, tokenizer.MaxInputBytes, tokenizer.MaxInputBytes + 1} {
		parser := tokenizerInputParser{bridge: &tokenizerInputBridge{}}
		input := "\x1b]11;" + strings.Repeat("x", size-5)
		err := parser.feed([]byte(input))
		if size <= tokenizer.MaxInputBytes {
			require.NoError(t, err)
		} else {
			require.EqualError(t, err, "incomplete terminal input sequence exceeds 1 MiB")
		}
		require.Equal(t, size, parser.ordinary.Len())
		require.Nil(t, parser.bridge.pending, "incomplete input must not create an acknowledgment")
	}
}

func TestTokenizerInputCompleteOrdinaryBytesPassUnchanged(t *testing.T) {
	for _, input := range []string{strings.Repeat("a", tokenizer.MaxInputBytes+1),
		"\x1b]999;" + strings.Repeat("x", tokenizer.MaxInputBytes-6) + "\a"} {
		reader, writer, err := os.Pipe()
		require.NoError(t, err)
		read := make(chan []byte, 1)
		readErr := make(chan error, 1)
		go func() {
			data, err := io.ReadAll(reader)
			read <- data
			readErr <- errors.Join(err, reader.Close())
		}()
		parser := tokenizerInputParser{bridge: &tokenizerInputBridge{output: writer}}
		err = parser.feed([]byte(input))
		closeErr := writer.Close()
		require.NoError(t, err)
		require.NoError(t, closeErr)
		require.Equal(t, input, string(<-read))
		require.NoError(t, <-readErr)
		require.Zero(t, parser.ordinary.Len())
	}
}

func TestTokenizerInputCancelIncompleteSequence(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	raw, writer := io.Pipe()
	defer writer.Close()
	source := &tokenizerTestInput{PipeReader: raw}
	bridge, err := startTokenizerInputBridge(ctx, source)
	require.NoError(t, err)
	defer bridge.Close()
	for _, chunk := range []string{"\x1b]11;", "unfinished", " response"} {
		_, err := io.WriteString(writer, chunk)
		require.NoError(t, err)
	}
	// The consumer never reads or acknowledges anything.
	require.NoError(t, bridge.Close())
	require.NoError(t, ctx.Err())
	require.Equal(t, int32(1), source.closes.Load())
}

func TestTokenizerInputExpiredEscapeQueuesSuffixBeforeResolve(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	raw, writer := io.Pipe()
	defer writer.Close()
	bridge, err := startTokenizerInputBridge(ctx, &tokenizerTestInput{PipeReader: raw})
	require.NoError(t, err)
	defer bridge.Close()
	events := make(chan uv.Event)
	decoded := make(chan error, 1)
	go func() {
		defer close(events)
		decoded <- uv.NewTerminalReader(bridge.Input(), "xterm-256color").StreamEvents(ctx, events)
	}()
	_, err = io.WriteString(writer, "\x1b")
	require.NoError(t, err)
	var text strings.Builder
	var keys []string
	queued := false
	for event := range events {
		var message tea.Msg = event
		switch event := event.(type) {
		case uv.PasteEvent:
			message = tea.PasteMsg(event)
			if !queued {
				// The decoder has expired ESC, but the event loop has not
				// resolved it. Queue the suffix before releasing the acknowledgment.
				_, err = io.WriteString(writer, "[200~typed\x03")
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				queued = true
			}
		case uv.KeyPressEvent:
			message = tea.KeyPressMsg(event)
		}
		if key, ok := bridge.Resolve(message).(tea.KeyPressMsg); ok {
			keys = append(keys, key.String())
			text.WriteString(key.Text)
		}
	}
	require.NoError(t, ctx.Err())
	require.NoError(t, <-decoded)
	require.True(t, queued)
	require.Equal(t, "[200~typed", text.String())
	require.Equal(t, "esc", keys[0])
	require.Equal(t, "ctrl+c", keys[len(keys)-1])
	require.ErrorIs(t, bridge.Close(), io.EOF)
}

func TestTokenizerInputReplacementRetiresOpenerPrefix(t *testing.T) {
	for end := 1; end < len(tokenizerPasteStart); end++ {
		messages := collectTokenizerInput(t, []string{tokenizerPasteStart[:end], "\ufffd", "[200~typed\x03",
			tokenizerPasteStart + "next" + tokenizerPasteEnd})
		var text strings.Builder
		var interrupted bool
		for _, message := range messages {
			if key, ok := message.(tea.KeyPressMsg); ok {
				text.WriteString(key.Text)
				interrupted = interrupted || key.String() == "ctrl+c"
			}
		}
		require.Equal(t, "\ufffd[200~typed", text.String())
		require.True(t, interrupted)
		require.Equal(t, tea.PasteMsg{Content: "next"}, messages[len(messages)-1])
	}
}

func TestTokenizerInputCloseWhileExpiredEscapeAwaitsResolve(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	raw, writer := io.Pipe()
	defer writer.Close()
	source := &tokenizerTestInput{PipeReader: raw}
	bridge, err := startTokenizerInputBridge(ctx, source)
	require.NoError(t, err)
	defer bridge.Close()
	_, err = io.WriteString(writer, "\x1b")
	require.NoError(t, err)
	marker := tokenizerPasteStart + tokenizerPastePlaceholder + tokenizerPasteEnd
	seen := make([]byte, len(marker))
	_, err = io.ReadFull(bridge.Input(), seen)
	require.NoError(t, err)
	require.Equal(t, marker, string(seen))
	// No Resolve call occurs. Close must release publication and join the reader.
	require.NoError(t, bridge.Close())
	require.NoError(t, ctx.Err())
	require.Equal(t, int32(1), source.closes.Load())
}

func TestTokenizerInputKeepsKeysAndPastesOrdered(t *testing.T) {
	wire := "a\t" + tokenizerPasteStart + "first\x03" + tokenizerPasteEnd + "b" + tokenizerPasteStart + "second" + tokenizerPasteEnd + "c"
	messages := collectTokenizerInput(t, []string{wire})
	require.Len(t, messages, 6)
	require.Equal(t, "a", messages[0].(tea.KeyPressMsg).Text)
	require.Equal(t, "tab", messages[1].(tea.KeyPressMsg).String())
	require.Equal(t, tea.PasteMsg{Content: "first\x03"}, messages[2])
	require.Equal(t, "b", messages[3].(tea.KeyPressMsg).Text)
	require.Equal(t, tea.PasteMsg{Content: "second"}, messages[4])
	require.Equal(t, "c", messages[5].(tea.KeyPressMsg).Text)
}

func TestTokenizerInputPreservesLiteralReplacementOutsidePaste(t *testing.T) {
	messages := collectTokenizerInput(t, []string{"a\xef", "\xbf", "\xbdb\xef", "\xbc\xa1"})
	var text strings.Builder
	for _, message := range messages {
		key, ok := message.(tea.KeyPressMsg)
		require.True(t, ok)
		text.WriteString(key.Text)
	}
	require.Equal(t, "a\ufffdbＡ", text.String())
}

func TestTokenizerInputLimitRejectsWholePasteAndRecovers(t *testing.T) {
	for _, size := range []int{tokenizer.MaxInputBytes, tokenizer.MaxInputBytes + 1, 4 * tokenizer.MaxInputBytes} {
		source := strings.Repeat("a", size)
		messages := collectTokenizerInput(t, []string{tokenizerPasteStart, source, tokenizerPasteEnd, tokenizerPasteStart + "next" + tokenizerPasteEnd})
		require.Len(t, messages, 2)
		if size == tokenizer.MaxInputBytes {
			require.Equal(t, tea.PasteMsg{Content: source}, messages[0])
		} else {
			require.IsType(t, tokenizerInputPasteErrorMsg{}, messages[0])
			m := testTokenizerEditor()
			m.insert("keep")
			m.Update(messages[0])
			require.Equal(t, "keep", m.text)
			require.Contains(t, m.note, "1 MiB")
		}
		require.Equal(t, tea.PasteMsg{Content: "next"}, messages[1])
	}
}

func TestTokenizerInputIncompletePasteDoesNotInsertPrefix(t *testing.T) {
	messages := collectTokenizerInput(t, []string{tokenizerPasteStart + "private incomplete\x1b[20"})
	require.Len(t, messages, 1)
	require.IsType(t, tokenizerInputPasteErrorMsg{}, messages[0])
	m := testTokenizerEditor()
	m.insert("keep")
	m.Update(messages[0])
	require.Equal(t, "keep", m.text)
	require.NotContains(t, m.note, "private")
	require.Contains(t, m.note, "interrupted")
}

func TestTokenizerInputIntentionalCloseJoinsBlockedReaders(t *testing.T) {
	for _, content := range []string{"", tokenizerPasteStart + "unfinished", tokenizerPasteStart + "pending" + tokenizerPasteEnd} {
		ctx, cancel := context.WithCancel(context.Background())
		raw, writer := io.Pipe()
		source := &tokenizerTestInput{PipeReader: raw}
		bridge, err := startTokenizerInputBridge(ctx, source)
		require.NoError(t, err)
		writeDone := make(chan struct{})
		go func() { defer close(writeDone); _, _ = io.WriteString(writer, content) }()
		if content != "" {
			// Drain bytes to reach capture or pending-paste acknowledgment.
			seen := make([]byte, len(tokenizerPasteStart))
			_, err = io.ReadFull(bridge.Input(), seen)
			require.NoError(t, err)
		}
		cancel()
		closed := make(chan error, 1)
		go func() { closed <- bridge.Close() }()
		select {
		case err := <-closed:
			require.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatal("input bridge failed to join after cancellation")
		}
		<-writeDone
		_ = writer.Close()
		require.Equal(t, int32(1), source.closes.Load())
		require.NoError(t, bridge.Close())
		require.NoError(t, bridge.Err())
	}
}

func TestTokenizerInputUnexpectedReadErrorRemainsObservable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	raw, writer := io.Pipe()
	bridge, err := startTokenizerInputBridge(ctx, &tokenizerTestInput{PipeReader: raw})
	require.NoError(t, err)
	failure := errors.New("synthetic read failure")
	require.NoError(t, writer.CloseWithError(failure))
	select {
	case <-bridge.Done():
	case <-time.After(time.Second):
		t.Fatal("read failure did not close Done")
	}
	require.ErrorIs(t, bridge.Err(), failure)
	require.ErrorIs(t, bridge.Close(), failure)
}

type tokenizerInputProgramModel struct {
	*tokenizerEditor
	bridge    *tokenizerInputBridge
	onMessage func(tea.Msg)
}

func (m *tokenizerInputProgramModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	message = m.bridge.Resolve(message)
	if m.onMessage != nil {
		m.onMessage(message)
	}
	_, cmd := m.tokenizerEditor.Update(message)
	return m, cmd
}

func TestTokenizerInputExpiredEscapeThroughBubbleTeaKeepsCtrlC(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	raw, writer := io.Pipe()
	defer writer.Close()
	bridge, err := startTokenizerInputBridge(ctx, &tokenizerTestInput{PipeReader: raw})
	require.NoError(t, err)
	defer bridge.Close()
	escape := make(chan struct{}, 1)
	model := &tokenizerInputProgramModel{tokenizerEditor: testTokenizerEditor(), bridge: bridge,
		onMessage: func(message tea.Msg) {
			if key, ok := message.(tea.KeyPressMsg); ok && key.String() == "esc" {
				escape <- struct{}{}
			}
		}}
	program := tea.NewProgram(model, tea.WithInput(bridge.Input()), tea.WithOutput(io.Discard),
		tea.WithContext(ctx), tea.WithWindowSize(80, 24), tea.WithoutRenderer(), tea.WithoutSignalHandler())
	written := make(chan error, 1)
	go func() {
		if _, err := io.WriteString(writer, "\x1b"); err != nil {
			written <- err
			return
		}
		select {
		case <-ctx.Done():
			written <- ctx.Err()
			return
		case <-escape:
		}
		_, err := io.WriteString(writer, "[200~typed\x03")
		written <- err
	}()
	_, err = program.Run()
	require.NoError(t, err)
	require.NoError(t, ctx.Err())
	require.NoError(t, <-written)
	require.True(t, model.quit)
	require.Equal(t, 130, model.exitCode)
	require.Equal(t, "[200~typed", model.text)
	require.NoError(t, bridge.Close())
}

func TestTokenizerInputThroughBubbleTeaPreservesExactText(t *testing.T) {
	for name, source := range map[string]string{
		"raw controls": "a\x1b[31m\ufffd\x00\r\n\t\x03b",
		"Unicode":      "日本語 e\u0301 👩‍💻",
		"invalid UTF8": "a\xffb",
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			raw, writer := io.Pipe()
			defer writer.Close()
			bridge, err := startTokenizerInputBridge(ctx, &tokenizerTestInput{PipeReader: raw})
			require.NoError(t, err)
			defer bridge.Close()
			model := &tokenizerInputProgramModel{tokenizerEditor: testTokenizerEditor(), bridge: bridge}
			program := tea.NewProgram(model, tea.WithInput(bridge.Input()), tea.WithOutput(io.Discard),
				tea.WithContext(ctx), tea.WithWindowSize(80, 24), tea.WithoutRenderer(), tea.WithoutSignalHandler())
			written := make(chan error, 1)
			go func() {
				_, err := io.WriteString(writer, tokenizerPasteStart+source+tokenizerPasteEnd+"\ufffd\x03")
				written <- err
			}()
			_, err = program.Run()
			require.NoError(t, err)
			require.NoError(t, ctx.Err())
			require.NoError(t, <-written)
			require.True(t, model.quit)
			require.Equal(t, 130, model.exitCode)
			if name == "invalid UTF8" {
				require.Equal(t, "\ufffd", model.text)
			} else {
				require.Equal(t, source+"\ufffd", model.text)
			}
			require.NoError(t, bridge.Close())
		})
	}
}
