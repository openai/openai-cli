package custom

import (
	"context"
	"errors"
	"io"
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
	bridge *tokenizerInputBridge
}

func (m *tokenizerInputProgramModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := m.tokenizerEditor.Update(m.bridge.Resolve(message))
	return m, cmd
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
