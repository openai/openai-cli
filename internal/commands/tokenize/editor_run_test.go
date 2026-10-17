package tokenize

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

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
