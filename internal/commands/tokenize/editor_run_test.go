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
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
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

func TestTokenizerEditorRestoresInlineModes(t *testing.T) {
	wrapEnabled := tea.ModeReportMsg{Mode: ansi.ModeAutoWrap, Value: ansi.ModeSet}
	wrapDisabled := tea.ModeReportMsg{Mode: ansi.ModeAutoWrap, Value: ansi.ModeReset}
	wrapUnavailable := tea.ModeReportMsg{Mode: ansi.ModeAutoWrap, Value: ansi.ModePermanentlyReset}
	inputError := tokenizerEditorInputErrorMsg{err: errors.New("synthetic input error")}
	for _, test := range []struct {
		name                       string
		messages                   []tea.Msg
		started, restoreWrap, fail bool
	}{
		{"closed before first reply", nil, false, false, false},
		{"wrapping unavailable", []tea.Msg{wrapUnavailable}, false, false, true},
		{"normal wrapping", []tea.Msg{wrapEnabled, tokenizerEditorWrapTimeout{}}, true, false, false},
		{"wrapping initially disabled", []tea.Msg{wrapDisabled}, true, true, false},
		{"input error after timeout", []tea.Msg{tokenizerEditorWrapTimeout{}, inputError}, true, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			paint := &tokenizerRecordedFrames{}
			inline := &tokenizerEditorInline{model: newTokenizerEditor(), painter: paint}
			inline.Init()
			for _, msg := range test.messages {
				inline.Update(msg)
			}
			require.Equal(t, test.fail, inline.err != nil)
			before, cleanup := paint.String(), inline.close()
			require.Empty(t, inline.close(), "restore modes only once")
			require.Equal(t, test.started, strings.Contains(before, ansi.SetModeAutoWrap))
			require.Equal(t, test.restoreWrap, strings.Contains(cleanup, ansi.ResetModeAutoWrap))
			require.Contains(t, cleanup, ansi.ResetModeBracketedPaste)
			require.Contains(t, cleanup, ansi.SetModeTextCursorEnable)
			require.NotContains(t, before+cleanup, ansi.EraseScreenBelow, "no frame owns any terminal rows yet")
		})
	}
}

func TestTokenizerStudioDividerRestoresCharacterSet(t *testing.T) {
	for _, dec := range []bool{false, true} {
		m := tokenizerEditorExample()
		m.width, m.height, m.decLineDrawing = 120, 30, dec
		inline := &tokenizerEditorInline{model: m, modesActive: true}
		frame := m.View().Content
		start := ansi.SelectCharacterSet('(', '0')
		end := ansi.SelectCharacterSet('(', 'B')
		if dec {
			// A complete frame must leave each pane in the ordinary character set.
			require.Contains(t, frame, start+"x"+end)
			require.Equal(t, strings.Count(frame, start), strings.Count(frame, start+"x"+end))
			// Cleanup must also recover when a write stops after the opening shift.
			require.Contains(t, inline.close(), end)
		} else {
			require.Contains(t, frame, ansi.CursorHorizontalAbsolute(m.sourceWidth()+4)+"│")
			require.NotContains(t, frame+inline.close(), start)
		}
	}
}

func TestTokenizerEditorRepaintsFromInlineAnchorOnResize(t *testing.T) {
	paint := &tokenizerRecordedFrames{}
	receipt := &tokenizerCursorOutput{FrameOutput: tokenizerFrameWriteFunc(func(_ context.Context, data []byte) (int, error) {
		return len(data), nil
	})}
	inline := &tokenizerEditorInline{model: newTokenizerEditor(), painter: paint, cursorOutput: receipt}
	inline.Init()
	inline.Update(tokenizerEditorWrapTimeout{})
	inline.Update(tea.ModeReportMsg{Mode: ansi.ModeAltScreenSaveCursor, Value: ansi.ModeSet})

	for i, size := range [][2]int{{120, 30}, {60, 18}, {100, 25}} {
		width, height := size[0], size[1]
		start := paint.Len()
		inline.drawFrame(width, height)
		frame := paint.String()[start:]
		require.Contains(t, frame, "Tokenizer")
		prefix := ansi.ResetModeTextCursorEnable
		if i > 0 {
			prefix += ansi.RestoreCursor
		}
		require.True(t, strings.HasPrefix(frame, prefix+"\r"+ansi.EraseScreenBelow))
		require.Equal(t, i, paint.replacements, "the initial origin must precede replaceable frames")
		rows := strings.Count(inline.content, "\n") + 1
		require.Less(t, rows, height, "leave room above the panel")
		c := inline.model.View().Cursor
		require.NotNil(t, c)
		placement := ansi.CursorDown(c.Y) + ansi.CursorHorizontalAbsolute(c.X+1) + ansi.SetCursorStyle(5) + ansi.SetModeTextCursorEnable
		require.True(t, strings.HasSuffix(frame, "\r"+ansi.CursorUp(rows-1)+ansi.SaveCursor+placement))
		_, err := receipt.WriteContext(t.Context(), []byte(frame))
		require.NoError(t, err)
	}

	cleanup := inline.close()
	require.Contains(t, cleanup, ansi.RestoreCursor+"\r"+ansi.EraseScreenBelow+ansi.SetCursorStyle(0))
	require.Contains(t, cleanup, ansi.SetModeTextCursorEnable)
	all := paint.String() + cleanup
	for _, control := range []string{ansi.SetModeAltScreenSaveCursor, ansi.RequestModeAltScreenSaveCursor,
		ansi.ResetModeAltScreenSaveCursor, ansi.CursorHomePosition, "\x1b[2J", "\x1b[3J"} {
		require.NotContains(t, all, control, "keep shell content above the panel")
	}
}

func TestTokenizerCursorOnlyMoveRepaintsCompleteFrame(t *testing.T) {
	m := newTokenizerEditor()
	m.insert("abcde")
	m.cursor = 3
	paint := &tokenizerRecordedFrames{}
	p := &tokenizerEditorInline{model: m, painter: paint}
	p.drawFrame(96, 24)
	content := p.content
	before := *p.cursor
	paint.Reset()
	tokenizerEditorKey(m, tea.KeyLeft)
	p.drawFrame(96, 24)
	require.Equal(t, content, p.content)
	require.Equal(t, before.X-1, p.cursor.X)
	require.Contains(t, paint.String(), ansi.ResetModeTextCursorEnable+ansi.RestoreCursor)
	require.Contains(t, paint.String(), "abcde")
	require.Contains(t, paint.String(), ansi.CursorHorizontalAbsolute(p.cursor.X+1)+ansi.SetCursorStyle(5))

	paint.Reset()
	p.drawFrame(96, 24)
	require.Empty(t, paint.String(), "an unchanged frame must leave native blinking undisturbed")
	tokenizerEditorKey(m, tea.KeyTab)
	p.drawFrame(96, 24)
	require.Nil(t, p.cursor)
	require.Contains(t, paint.String(), ansi.ResetModeTextCursorEnable+ansi.RestoreCursor)
	require.NotContains(t, paint.String(), ansi.SetModeTextCursorEnable)
}

func TestTokenizerCaretFollowsTerminalWidthAfterJoinedEmoji(t *testing.T) {
	for _, size := range []struct {
		name          string
		width, height int
		prefix        string
	}{
		{"wide", 120, 30, "a"},
		{"soft-wrapped", 96, 16, strings.Repeat("a", 40)},
		{"horizontally clipped", 40, 12, strings.Repeat("a", 55)},
	} {
		for _, profile := range []colorprofile.Profile{colorprofile.TrueColor, colorprofile.ASCII} {
			t.Run(size.name+"/"+profile.String(), func(t *testing.T) {
				m := newTokenizerEditor()
				m.color = profile == colorprofile.TrueColor
				draft := size.prefix + "👩‍💻Ze\u0301b"
				m.insert(draft)
				m.cursor = strings.IndexByte(draft, 'Z')
				paint := &tokenizerRecordedFrames{}
				p := &tokenizerEditorInline{model: m, painter: paint, profile: profile}
				p.drawFrame(size.width, size.height)
				require.NotNil(t, p.cursor)
				line := strings.Split(p.content, "\n")[p.cursor.Y]
				prefix, _, found := strings.Cut(line, "Z")
				require.True(t, found, "the character under the caret must be visible")
				require.True(t, strings.HasSuffix(prefix, "👩‍💻"))
				require.Equal(t, m.color, prefix != ansi.Strip(prefix))
				placement := ansi.CursorDown(p.cursor.Y) + ansi.ResetModeAutoWrap + prefix + ansi.SetModeAutoWrap
				require.True(t, strings.HasSuffix(paint.String(), ansi.SaveCursor+placement+ansi.SetCursorStyle(5)+ansi.SetModeTextCursorEnable),
					"replay the exact painted prefix up to Z, including styles and excluding pane controls")
				require.Equal(t, draft, m.text)
			})
		}
	}
}

func TestTokenizerRestoresReportedShellCursorStyle(t *testing.T) {
	const query = "\x1bP$q q\x1b\\"
	const underline = "\x1bP1$r3 q\x1b\\"
	const bar = "\x1bP1$r6 q\x1b\\"
	for _, test := range []struct {
		name    string
		replies []string
		style   int
	}{
		{"silent terminal", nil, 0},
		{"unsupported reply", []string{"\x1bP0$r3 q\x1b\\"}, 0},
		{"first valid reply", []string{"\x1bP1$r2m\x1b\\", "\x1bP1$r9 q\x1b\\", underline, bar}, 3},
		{"reported default", []string{"\x1bP1$r0 q\x1b\\", bar}, 0},
		{"eight-bit reply", []string{"\x901$r4 q\x9c"}, 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := testTokenizerEditor()
			m.insert("draft")
			revision := m.revision
			paint := &tokenizerRecordedFrames{}
			inline := &tokenizerEditorInline{model: m, painter: paint}
			inline.Init()
			require.Equal(t, 1, strings.Count(paint.String(), query), "request the shell style before changing it")
			require.NotContains(t, paint.String(), ansi.SetCursorStyle(5))
			inline.drawFrame(80, 24)
			require.Contains(t, paint.String(), ansi.SetCursorStyle(5), "Text uses a blinking bar")
			var decoder uv.EventDecoder
			for _, reply := range test.replies {
				n, event := decoder.Decode([]byte(reply))
				require.Equal(t, len(reply), n)
				require.IsType(t, uv.UnknownDcsEvent(""), event)
				inline.Update(event)
			}
			require.Equal(t, "draft", m.text)
			require.Equal(t, revision, m.revision)
			cleanup := inline.close()
			for style := 0; style <= 6; style++ {
				require.Equal(t, style == test.style, strings.Contains(cleanup, ansi.SetCursorStyle(style)), "cleanup style %d", style)
			}
		})
	}
}

func TestTokenizerCursorCleanupRequiresAcknowledgedOrigin(t *testing.T) {
	replay := "panel" + ansi.SaveCursor + ansi.ResetModeAutoWrap
	frame := []byte(replay + "👩‍💻" + ansi.SetModeAutoWrap)
	for _, n := range []int{0, len("panel"), len("panel") + 1, len("panel" + ansi.SaveCursor), len(replay), len(frame)} {
		receipt := &tokenizerCursorOutput{FrameOutput: tokenizerFrameWriteFunc(func(context.Context, []byte) (int, error) {
			if n < len(frame) {
				return n, io.ErrShortWrite
			}
			return n, nil
		})}
		accepted, err := receipt.WriteContext(t.Context(), frame)
		require.Equal(t, n, accepted)
		require.Equal(t, n < len(frame), errors.Is(err, io.ErrShortWrite))
		saved := n >= len("panel"+ansi.SaveCursor)
		for _, originallyDisabled := range []bool{false, true} {
			p := &tokenizerEditorInline{modesActive: true, restoreWrap: originallyDisabled, cursorStyled: true, cursorOutput: receipt}
			cleanup := p.close()
			require.Equal(t, saved, strings.Contains(cleanup, ansi.RestoreCursor))
			require.Equal(t, saved, strings.Contains(cleanup, ansi.EraseScreenBelow))
			require.Contains(t, cleanup, ansi.SetCursorStyle(0))
			require.Equal(t, originallyDisabled, strings.Contains(cleanup, ansi.ResetModeAutoWrap))
			require.Equal(t, !originallyDisabled, strings.Contains(cleanup, ansi.SetModeAutoWrap))
			require.Empty(t, p.close())
		}
	}
}

func TestTokenizerResizeTracksTheLastWrittenCaret(t *testing.T) {
	width, height := 120, 30
	var output bytes.Buffer
	receipt := &tokenizerCursorOutput{
		FrameOutput: tokenizerFrameWriteFunc(func(_ context.Context, data []byte) (int, error) { return output.Write(data) }),
		size:        func() (int, int, error) { return width, height, nil },
	}
	m := testTokenizerEditor()
	m.insert("first\nsecond\nthird")
	paint := &tokenizerRecordedFrames{}
	p := &tokenizerEditorInline{model: m, painter: paint, cursorOutput: receipt}
	p.drawFrame(width, height)
	_, err := receipt.WriteContext(t.Context(), []byte(paint.String()))
	require.NoError(t, err)
	paintedY := p.cursor.Y
	m.cursor = 0
	p.drawFrame(width, height) // This replacement is dropped before the writer receives it.
	require.NotEqual(t, paintedY, p.cursor.Y)
	cleanup := func() string {
		return (&tokenizerEditorInline{modesActive: true, cursorOutput: receipt}).close()
	}
	for _, size := range [][2]int{{40, 30}, {140, 30}, {120, 40}} {
		width, height = size[0], size[1]
		require.Contains(t, cleanup(), ansi.RestoreCursor+"\r"+ansi.EraseScreenBelow)
	}
	recoverOrigin := "\r" + ansi.CursorUp(paintedY)
	for _, newWidth := range []int{40, 120, 140} {
		width, height = newWidth, 12
		require.Contains(t, cleanup(), recoverOrigin+"\r"+ansi.EraseScreenBelow, "resize followed immediately by Ctrl+C at width %d", width)
	}
	output.Reset()
	paint.Reset()
	p.drawFrame(width, height)
	_, err = receipt.WriteContext(t.Context(), []byte(paint.String()))
	require.NoError(t, err)
	require.Equal(t, ansi.ResetModeTextCursorEnable+recoverOrigin+ansi.SaveCursor+paint.String(), output.String())
	require.Contains(t, cleanup(), ansi.RestoreCursor+"\r"+ansi.EraseScreenBelow, "the resized frame established its origin")
}

type tokenizerFrameWriteFunc func(context.Context, []byte) (int, error)

func (f tokenizerFrameWriteFunc) WriteContext(ctx context.Context, data []byte) (int, error) {
	return f(ctx, data)
}

type tokenizerRecordedFrames struct {
	strings.Builder
	replacements int
}

func (p *tokenizerRecordedFrames) Control(data string) { _, _ = p.WriteString(data) }
func (p *tokenizerRecordedFrames) Frame(data string) {
	p.replacements++
	_, _ = p.WriteString(data)
}

type tokenizerPartialSink struct {
	accepted bytes.Buffer
	failure  error
}

func (s *tokenizerPartialSink) Write(data []byte) (int, error) {
	n := min(len(data)/2, 19)
	_, _ = s.accepted.Write(data[:n])
	return n, s.failure
}
