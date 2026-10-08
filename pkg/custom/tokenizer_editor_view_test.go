package custom

import (
	"encoding/hex"
	"fmt"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func tokenizerEditorExample() *tokenizerEditor {
	m := testTokenizerEditor()
	m.insert("Hello, world!")
	m.encoding = "cl100k_base"
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{
		{ID: 9906, EndByte: 5}, {ID: 11, EndByte: 6}, {ID: 1917, EndByte: 12}, {ID: 0, EndByte: 13},
	}})
	m.selected = 2
	return m
}

func TestTokenizerEditorViewFitsEverySupportedSizeAndFocus(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}, {60, 16}, {40, 12}} {
		for focus := 0; focus < 3; focus++ {
			t.Run(fmt.Sprintf("%dx%d/focus%d", size[0], size[1], focus), func(t *testing.T) {
				m := tokenizerEditorExample()
				m.width, m.height, m.focus = size[0], size[1], focus
				view := m.View()
				require.False(t, view.AltScreen)
				require.Contains(t, view.Content, "4 tokens · 13 bytes")
				require.Contains(t, view.Content, "Token IDs")
				require.Contains(t, view.Content, "Bytes")
				require.Contains(t, view.Content, "ID 1917")
				require.Contains(t, view.Content, "[6, 12)")
				require.Contains(t, view.Content, "Ctrl+C exit")
				require.Contains(t, view.Content, "Encoding")
				lines := strings.Split(view.Content, "\n")
				require.LessOrEqual(t, len(lines), m.viewHeight())
				for _, line := range lines {
					require.Less(t, ansi.StringWidth(line), m.width, line)
				}
			})
		}
	}
}

func TestTokenizerEditorViewsShareSelectionAndExactDetails(t *testing.T) {
	m := tokenizerEditorExample()
	m.focus = 1
	for tab, expected := range []string{`" world"`, "[1917]", "20 77 6f 72 6c"} {
		m.tab = tab
		view := m.View().Content
		require.Contains(t, view, expected)
		require.Contains(t, view, "Token 3/4 · ID 1917")
		require.Contains(t, view, "[6, 12)")
		require.Contains(t, view, "20 77 6f 72 6c 64")
	}
}

func TestTokenizerEditorCompactSelectionIsAlwaysVisible(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height, m.focus = 40, 12, 1
	m.insert(strings.Repeat("long-fragment-", 5))
	tokens := make([]tokenizerPreviewToken, 5)
	for i := range tokens {
		tokens[i] = tokenizerPreviewToken{ID: uint32(i), EndByte: uint32((i + 1) * len("long-fragment-"))}
	}
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
	for i := range tokens {
		m.selected = i
		for tab := 0; tab < 3; tab++ {
			m.tab = tab
			require.Contains(t, strings.Join(m.resultLines(m.styles(), m.viewWidth()), "\n"), "›[")
		}
	}
}

func TestTokenizerEditorShowsWholeSequenceWhenItFits(t *testing.T) {
	m := tokenizerEditorExample()
	m.focus, m.selected, m.tokenStart = 1, 3, 3
	for tab := 0; tab < 3; tab++ {
		m.tab = tab
		require.Zero(t, m.resultStart(m.viewWidth()))
		_, count := m.resultWindow(m.styles(), m.viewWidth())
		require.Equal(t, len(m.tokens), count)
	}
	// Widening also restores earlier tokens when the full sequence now fits.
	m.width, m.height, m.tab = 40, 24, 2
	m.keepSelectionVisible()
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	require.Zero(t, m.tokenStart)
}

func TestTokenizerEditorTokenWindowStaysStillUntilSelectionLeaves(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height, m.focus, m.tab = 40, 24, 1, 1
	m.insert(strings.Repeat("a", 100))
	tokens := make([]tokenizerPreviewToken, 100)
	for i := range tokens {
		tokens[i] = tokenizerPreviewToken{ID: uint32(i), EndByte: uint32(i + 1)}
	}
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
	for next := 1; next < 5; next++ {
		tokenizerEditorKey(m, tea.KeyDown)
		require.Equal(t, next, m.selected)
		require.Zero(t, m.tokenStart)
		require.Contains(t, strings.Join(m.resultLines(m.styles(), m.viewWidth()), ""), "[0]")
	}
	tokenizerEditorKey(m, tea.KeyEnd)
	require.Positive(t, m.tokenStart)
	start := m.tokenStart
	tokenizerEditorKey(m, tea.KeyUp)
	require.Equal(t, start, m.tokenStart)
	tokenizerEditorKey(m, tea.KeyHome)
	require.Zero(t, m.tokenStart)
}

func TestTokenizerEditorCompactResultHintsRemainComplete(t *testing.T) {
	m := tokenizerEditorExample()
	m.width, m.height, m.focus = 40, 24, 1
	view := m.View().Content
	require.Contains(t, view, "Enter details · Tab next · ? help")
	require.Contains(t, view, "Ctrl+C exit")
}

func TestTokenizerEditorLeavesEmptySourceRowsUnfilled(t *testing.T) {
	m := tokenizerEditorExample()
	m.focus, m.color = 0, true
	for _, dark := range []bool{false, true} {
		m.dark = dark
		rows := strings.Split(m.View().Content, "\n")
		require.Regexp(t, `\x1b\[(?:[0-9]+;)*48;2;`, rows[2], "the nonempty typing row retains the image theme fill")
		require.NotRegexp(t, `\x1b\[(?:[0-9]+;)*48;2;`, rows[3], "empty padding rows must remain unfilled")
		require.NotRegexp(t, `\x1b\[(?:[0-9]+;)*48;2;`, rows[4], "empty padding rows must remain unfilled")
	}
	m.insert("\n")
	rows := strings.Split(m.View().Content, "\n")
	require.NotRegexp(t, `\x1b\[(?:[0-9]+;)*48;2;`, rows[3], "an empty cursor row must remain unfilled")
}

func TestTokenizerEditorRendersControlsWithoutExecutingThem(t *testing.T) {
	m := testTokenizerEditor()
	m.insert("A\x1b[31m\x00\r\nB\t\u202eC")
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{{ID: 1, EndByte: uint32(len(m.text))}}})
	for modal := 0; modal < 3; modal++ {
		m.modal = modal
		view := m.View().Content
		view = strings.ReplaceAll(view, ansi.EraseCharacter(m.viewWidth()-2), "")
		view = strings.ReplaceAll(view, ansi.CursorHorizontalAbsolute(m.viewWidth()+2), "")
		require.NotContains(t, view, "\x1b")
		require.NotContains(t, view, "\x00")
		require.NotContains(t, view, "\r")
		require.NotContains(t, view, "\u202e")
		require.NotContains(t, view, "�")
	}
}

func TestTokenizerEditorPartialUTF8UsesLosslessBytes(t *testing.T) {
	m := testTokenizerEditor()
	m.insert("😀")
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{
		{ID: 1, EndByte: 1}, {ID: 2, EndByte: 4},
	}})
	m.focus = 1
	for _, size := range [][2]int{{80, 24}, {40, 12}} {
		m.width, m.height = size[0], size[1]
		for i, expected := range []string{"f0", "9f 98 80"} {
			m.selected = i
			view := m.View().Content
			require.Contains(t, view, "partial UTF-8")
			require.Contains(t, view, expected)
			require.NotContains(t, view, "�")
			m.modal = 2
			view = m.View().Content
			require.Contains(t, view, expected)
			m.modal = 0
		}
	}
}

func TestTokenizerEditorDetailsExposeEveryByteWithBoundedFrames(t *testing.T) {
	m := testTokenizerEditor()
	source := strings.Repeat("e\u0301👩‍💻\r\n", 128)
	m.insert(source)
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{{ID: 1, EndByte: uint32(len(source))}}})
	m.modal, m.width, m.height = 2, 40, 12
	_, total := m.modalRows(0, 0)
	var recovered strings.Builder
	hexStarted := false
	for start := 0; start < total; start += 8 {
		rows, gotTotal := m.modalRows(start, 8)
		require.Equal(t, total, gotTotal)
		require.LessOrEqual(t, len(rows), 8)
		for _, row := range rows {
			require.LessOrEqual(t, ansi.StringWidth(row), m.viewWidth())
			if row == "Hex:" {
				hexStarted = true
				continue
			}
			if hexStarted {
				recovered.WriteString(strings.ReplaceAll(row, " ", ""))
			}
		}
		m.scroll = start
		frame := m.View().Content
		require.Less(t, len(frame), 4096)
		require.LessOrEqual(t, len(strings.Split(frame, "\n")), m.viewHeight())
	}
	decoded, err := hex.DecodeString(recovered.String())
	require.NoError(t, err)
	require.Equal(t, source, string(decoded))
	tokenizerEditorKey(m, tea.KeyEnd)
	last := m.View().Content
	require.Contains(t, last, fmt.Sprintf("of %d", total))
	old := m.scroll
	tokenizerEditorKey(m, tea.KeyUp)
	require.Less(t, m.scroll, old)
}

func TestTokenizerEditorRenderingBoundsLongCombiningCluster(t *testing.T) {
	m := testTokenizerEditor()
	m.insert("a" + strings.Repeat("\u0301", 32*1024))
	require.Less(t, len(m.View().Content), 4096)
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{{ID: 1, EndByte: uint32(len(m.text))}}})
	m.modal = 2
	require.Less(t, len(m.View().Content), 4096)
	rows, _ := m.modalRows(0, 10)
	for _, row := range rows {
		require.LessOrEqual(t, len(row), m.viewWidth()+16)
	}
}

func TestTokenizerEditorUpdatingNeverShowsOldCount(t *testing.T) {
	m := tokenizerEditorExample()
	m.insert("new")
	view := m.View().Content
	require.Contains(t, view, "Updating")
	require.NotContains(t, view, "4 tokens")
	require.NotContains(t, view, "ID 1917")
}

func TestTokenizerEditorNoColorKeepsSelectionAndNavigation(t *testing.T) {
	m := tokenizerEditorExample()
	m.focus = 1
	m.color = false
	for _, dark := range []bool{false, true} {
		m.dark = dark
		view := m.View().Content
		require.NotRegexp(t, `\x1b\[[0-9;]*m`, view)
		require.Contains(t, view, "[Text]")
		require.Contains(t, view, "›[")
		require.Contains(t, view, "←→ view")
	}
}

func TestTokenizerEditorLargeFrameWorkStaysBounded(t *testing.T) {
	for _, size := range []int{1 << 10, 64 << 10, 1 << 20} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := testTokenizerEditor()
			m.insert(strings.Repeat("a", size-len("tail")) + "tail")
			m.View() // Initialize shared formatting before measuring one repaint.
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			frame := m.View().Content
			runtime.ReadMemStats(&after)
			require.Contains(t, frame, "tail▏")
			require.Less(t, len(frame), 4096)
			require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(256<<10), "repaint must not allocate per source grapheme")
		})
	}
}

func TestTokenizerEditorCompactErrorsKeepCompleteRecovery(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height = 40, 12
	m.insert("keep")
	m.insert(string([]byte{0xff}))
	frame := m.View().Content
	normalized := strings.Join(strings.Fields(frame), " ")
	require.Contains(t, frame, "Paste rejected")
	require.Contains(t, normalized, "valid UTF-8")
	require.Contains(t, frame, "unchanged")
	require.Contains(t, frame, "Ctrl+C exit")
	require.NotContains(t, strings.Join(strings.Fields(frame), " "), "unchanged…")
	// An unrelated successful computation must not erase rejection guidance.
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{{ID: 1, EndByte: 4}}})
	require.Contains(t, m.View().Content, "unchanged")
}

func TestTokenizerEditorTinyFallbackShowsOnlyCompleteCommands(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height = 39, 10
	require.Contains(t, m.View().Content, "openai tokenizer --format text")
	m.invocation = "'/a long path/openai'"
	require.NotContains(t, m.View().Content, "Plain:")
	m.width = 80
	require.Contains(t, m.View().Content, "'/a long path/openai' tokenizer --format text")
	m.width = 20
	require.Contains(t, m.View().Content, "Ctrl+C exit")
	require.NotContains(t, m.View().Content, "Plain:")
}

func TestTokenizerEditorLargeTokenJumpUsesBoundedWindow(t *testing.T) {
	m := testTokenizerEditor()
	m.focus = 1
	m.insert(strings.Repeat("a", 1<<20))
	tokens := make([]tokenizerPreviewToken, len(m.text))
	for i := range tokens {
		tokens[i] = tokenizerPreviewToken{ID: uint32(i), EndByte: uint32(i + 1)}
	}
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	tokenizerEditorKey(m, tea.KeyEnd)
	frame := m.View().Content
	runtime.ReadMemStats(&after)
	require.Equal(t, len(tokens)-1, m.selected)
	require.Contains(t, frame, "ID 1048575")
	require.Less(t, len(frame), 4096)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(256<<10), "jumping must not format the skipped tokens")
}
