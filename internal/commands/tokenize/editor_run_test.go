package tokenize

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/tokenizer"
	"github.com/stretchr/testify/require"
)

func TestTokenizerEditorCountsEachWarmEditBeforeUpdateReturns(t *testing.T) {
	worker := newTokenizerPreviewWorker(t.Context(), os.Args[0])
	t.Cleanup(func() { _ = worker.Close() })
	m := testTokenizerEditor()
	p := &tokenizerEditorInline{model: m, worker: worker}
	prepareDefault := p.prepareEncoding()
	p.Update(tea.PasteMsg{Content: "how"})
	p.Update(tea.KeyPressMsg{Code: tea.KeyF3})
	p.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "cl100k_base", m.encoding)
	_, prepareCurrent := p.Update(prepareDefault())
	require.NotNil(t, prepareCurrent, "finish preparing the newly selected encoding")
	require.Empty(t, m.tokens)
	p.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	p.Update(tea.PasteMsg{Content: " are"})
	p.Update(prepareCurrent())

	checkExact := func() {
		t.Helper()
		want, err := tokenizer.Encode(m.text, m.encoding, true)
		require.NoError(t, err)
		require.False(t, m.updating)
		require.Len(t, m.tokens, want.TokenCount)
		end := 0
		for i, fragment := range want.Fragments {
			end += len(fragment)
			require.Equal(t, tokenizerPreviewToken{ID: uint32(want.IDs[i]), EndByte: uint32(end)}, m.tokens[i])
		}
	}
	checkExact()
	for _, letter := range " you 👋" {
		p.Update(tea.KeyPressMsg{Code: letter, Text: string(letter)})
		checkExact()
	}
	p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	checkExact()
}

func TestTokenizerEditorSwitchesBetweenInlineAndIsolatedCounts(t *testing.T) {
	t.Setenv("OPENAI_TEST_TOKENIZER_PREVIEW", "worker")
	worker := newTokenizerPreviewWorker(t.Context(), os.Args[0])
	t.Cleanup(func() { _ = worker.Close() })
	m := testTokenizerEditor()
	p := &tokenizerEditorInline{model: m, worker: worker}
	p.Update(p.prepareEncoding()())
	p.Update(tea.PasteMsg{Content: strings.Repeat("a\n", tokenizerInlineBytes/2)})
	require.False(t, m.updating)
	require.Len(t, m.tokens, tokenizerInlineBytes)

	p.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	require.True(t, m.updating)
	require.Empty(t, m.tokens)
	var pending tokenizerPreviewResult
	select {
	case pending = <-worker.Results():
		require.NoError(t, pending.Err)
		require.Equal(t, m.revision, pending.Revision)
		require.Len(t, pending.Tokens, tokenizerInlineBytes+1)
	case <-time.After(20 * time.Second):
		t.Fatal("worker did not return the larger draft")
	}
	p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	p.Update(tokenizerEditorResultMsg{Revision: pending.Revision, Tokens: pending.Tokens})
	require.False(t, m.updating)
	require.Len(t, m.tokens, tokenizerInlineBytes)
	require.Equal(t, uint32(tokenizerInlineBytes), m.tokens[len(m.tokens)-1].EndByte)
}

func TestTokenizerEditorFallbackOutputFailure(t *testing.T) {
	for _, failure := range []error{io.ErrClosedPipe, nil, context.Canceled} {
		sink := &tokenizerPartialSink{failure: failure}
		root := tokenizerTestRoot(tokenizerRejectReader{}, sink)
		err := root.Run(t.Context(), []string{"openai", "tokenizer"})
		want := failure
		if want == nil {
			want = io.ErrShortWrite
		}
		if !errors.Is(err, want) {
			t.Fatalf("lost failed fallback output: got %v, want %v", err, want)
		}
	}
}

func TestTokenizerEditorArgumentRecoveryNamesSupportedCommand(t *testing.T) {
	var output bytes.Buffer
	root := tokenizerTestRoot(tokenizerRejectReader{}, &output)
	err := root.Run(t.Context(), []string{"openai", "tokenizer", "synthetic-private-input"})
	if err == nil || !strings.Contains(err.Error(), "tokenizer count --text TEXT") {
		t.Fatalf("missing supported input recovery: %v", err)
	}
	if output.Len() != 0 || strings.Contains(err.Error(), "synthetic-private") {
		t.Fatal("invalid parent input produced output or exposed input")
	}
	root = tokenizerTestRoot(tokenizerRejectReader{}, &output)
	if err := root.Run(t.Context(), []string{"openai", "tokenizer", "count", "--text", "TEXT"}); err != nil {
		t.Fatalf("suggested recovery failed: %v", err)
	}
}

func TestTokenizerEditorFormatRecoveryDistinguishesScriptCommands(t *testing.T) {
	for _, flags := range [][]string{{"--raw-output"}, {"--transform", ".private"}, {"--format", "yaml"}} {
		root := tokenizerTestRoot(tokenizerRejectReader{}, io.Discard)
		err := root.Run(t.Context(), append([]string{"openai", "tokenizer"}, flags...))
		if err == nil || !strings.Contains(err.Error(), "For JSON, use tokenizer count or inspect.") {
			t.Fatalf("format recovery does not name supported JSON commands: %v", err)
		}
	}
}

func TestTokenizerEditorModesRestoreOnEarlyExit(t *testing.T) {
	for _, started := range []bool{false, true} {
		for _, restoreWrap := range []bool{false, true} {
			paint := &tokenizerRecordedFrames{}
			inline := &tokenizerEditorInline{model: newTokenizerEditor(), painter: paint}
			_ = inline.Init()
			inline.started, inline.restoreWrap = started, restoreWrap
			data := []byte(paint.String() + inline.close() + inline.close())
			for _, control := range []string{ansi.ResetModeBracketedPaste, ansi.SetModeTextCursorEnable} {
				if bytes.Count(data, []byte(control)) != 1 {
					t.Fatalf("cleanup did not restore %q exactly once", control)
				}
			}
			if strings.Contains(string(data), ansi.ResetModeAutoWrap) != restoreWrap {
				t.Fatal("cleanup changed initial wrapping state")
			}
			if strings.Contains(string(data), ansi.EraseScreenBelow) != started {
				t.Fatal("cleanup cleared before the first frame")
			}
		}
	}
}

type tokenizerRecordedFrames struct{ strings.Builder }

func (p *tokenizerRecordedFrames) Control(data string) { _, _ = p.WriteString(data) }
func (p *tokenizerRecordedFrames) Frame(data string)   { _, _ = p.WriteString(data) }

type tokenizerPartialSink struct {
	accepted bytes.Buffer
	failure  error
}

func (s *tokenizerPartialSink) Write(data []byte) (int, error) {
	n := min(len(data)/2, 19)
	_, _ = s.accepted.Write(data[:n])
	return n, s.failure
}
