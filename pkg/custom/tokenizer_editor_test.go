package custom

import (
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-cli/internal/tokenizer"
	"github.com/stretchr/testify/require"
)

func testTokenizerEditor() *tokenizerEditor {
	m := newTokenizerEditor()
	m.width, m.height, m.color = 80, 24, false
	return m
}

func tokenizerEditorKey(m *tokenizerEditor, code rune) tea.Cmd {
	_, cmd := m.Update(tea.KeyPressMsg{Code: code})
	return cmd
}

func TestTokenizerEditorPreservesPastedBytes(t *testing.T) {
	m := testTokenizerEditor()
	source := "\ufeffa\r\nb\t\x00\x1b[31m e\u0301 👩‍💻\n"
	_, cmd := m.Update(tea.PasteMsg{Content: source})
	require.NotNil(t, cmd)
	require.Equal(t, source, m.text)
	require.Equal(t, len(source), m.cursor)
	require.True(t, m.updating)
	_, cmd = m.Update(tokenizerEditorDebounceMsg{revision: m.revision})
	require.NotNil(t, cmd)
	request := cmd().(tokenizerEditorRequestMsg)
	require.Equal(t, source, request.Text)
	require.Equal(t, "o200k_base", request.Encoding)
	require.Equal(t, m.revision, request.Revision)
}

func TestTokenizerEditorRejectsPasteWithoutChangingDraft(t *testing.T) {
	for name, source := range map[string]string{"invalid": string([]byte{0xff}), "large": strings.Repeat("x", tokenizer.MaxInputBytes)} {
		t.Run(name, func(t *testing.T) {
			m := testTokenizerEditor()
			m.insert("keep")
			previous := m.revision
			_, cmd := m.Update(tea.PasteMsg{Content: source})
			require.Nil(t, cmd)
			require.Equal(t, "keep", m.text)
			require.Equal(t, 4, m.cursor)
			require.Equal(t, previous, m.revision)
			require.Contains(t, m.note, "unchanged")
		})
	}
}

func TestTokenizerEditorMovesAndDeletesCompleteGraphemes(t *testing.T) {
	for _, cluster := range []string{"é", "e\u0301", "👩‍💻", "🇨🇦", "1️⃣", "\r\n"} {
		t.Run(cluster, func(t *testing.T) {
			m := testTokenizerEditor()
			m.insert("a" + cluster + "z")
			tokenizerEditorKey(m, tea.KeyLeft)
			require.Equal(t, 1+len(cluster), m.cursor)
			tokenizerEditorKey(m, tea.KeyLeft)
			require.Equal(t, 1, m.cursor)
			tokenizerEditorKey(m, tea.KeyDelete)
			require.Equal(t, "az", m.text)
			require.Equal(t, 1, m.cursor)
			m.insert(cluster)
			tokenizerEditorKey(m, tea.KeyBackspace)
			require.Equal(t, "az", m.text)
		})
	}
}

func TestTokenizerEditorMultilineCursorAndEnter(t *testing.T) {
	m := testTokenizerEditor()
	m.insert("ab\r\nαβ\nxyz")
	tokenizerEditorKey(m, tea.KeyHome)
	tokenizerEditorKey(m, tea.KeyRight)
	require.Equal(t, len("ab\r\nαβ\nx"), m.cursor)
	tokenizerEditorKey(m, tea.KeyUp)
	require.Equal(t, len("ab\r\nα"), m.cursor)
	tokenizerEditorKey(m, tea.KeyUp)
	require.Equal(t, 1, m.cursor)
	tokenizerEditorKey(m, tea.KeyEnd)
	require.Equal(t, 2, m.cursor)
	tokenizerEditorKey(m, tea.KeyDown)
	require.Equal(t, len("ab\r\nαβ"), m.cursor)
	tokenizerEditorKey(m, tea.KeyEnter)
	require.Equal(t, "ab\r\nαβ\n\nxyz", m.text)
}

func TestTokenizerEditorClearsPrefixAndRetiresResults(t *testing.T) {
	m := testTokenizerEditor()
	m.insert("First\r\ne\u0301👩‍💻 tail")
	m.cursor = len("First\r\ne\u0301👩‍💻")
	old := m.revision
	m.Update(tokenizerEditorResultMsg{Revision: old, Tokens: []tokenizerPreviewToken{{ID: 1, EndByte: uint32(len(m.text))}}})
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	require.Equal(t, " tail", m.text)
	require.Zero(t, m.cursor)
	require.Greater(t, m.revision, old)
	require.Empty(t, m.tokens)
	m.Update(tokenizerEditorResultMsg{Revision: old, Tokens: []tokenizerPreviewToken{{ID: 1, EndByte: 1}}})
	require.Empty(t, m.tokens)
	m.cursor = len(m.text)
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	require.Empty(t, m.text)
	require.False(t, m.updating)
	require.Equal(t, []int{0}, m.boundaries)
}

func TestTokenizerEditorIgnoresStaleWorkAndClearsOldCounts(t *testing.T) {
	m := testTokenizerEditor()
	m.insert("one")
	old := m.revision
	m.Update(tokenizerEditorResultMsg{Revision: old, Tokens: []tokenizerPreviewToken{{ID: 1, EndByte: 3}}})
	require.Len(t, m.tokens, 1)
	m.insert(" two")
	require.Empty(t, m.tokens)
	require.True(t, m.updating)
	_, cmd := m.Update(tokenizerEditorDebounceMsg{revision: old})
	require.NotNil(t, cmd)
	m.Update(tokenizerEditorResultMsg{Revision: old, Tokens: []tokenizerPreviewToken{{ID: 2, EndByte: 3}}})
	require.Empty(t, m.tokens)
	require.True(t, m.updating)
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{{ID: 1, EndByte: 3}, {ID: 2, EndByte: 7}}})
	require.Len(t, m.tokens, 2)
	require.False(t, m.updating)
}

func TestTokenizerEditorRejectsBadResultBoundaries(t *testing.T) {
	for _, tokens := range [][]tokenizerPreviewToken{
		nil, {{ID: 1, EndByte: 0}}, {{ID: 1, EndByte: 4}},
		{{ID: 1, EndByte: 2}}, {{ID: 1, EndByte: 2}, {ID: 2, EndByte: 1}},
	} {
		m := testTokenizerEditor()
		m.insert("abc")
		m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
		require.Empty(t, m.tokens)
		require.True(t, m.failed)
		require.False(t, m.updating)
	}
}

func TestTokenizerEditorFailureRecoveryKeepsDraft(t *testing.T) {
	m := testTokenizerEditor()
	m.insert("private text")
	m.Update(tokenizerEditorDebounceMsg{revision: m.revision})
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Err: errors.New("secret failure")})
	require.Equal(t, "private text", m.text)
	require.True(t, m.failed)
	require.NotContains(t, m.note, "secret")
	m.focus = tokenizerFocusResults
	previous := m.revision
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	require.NotNil(t, cmd)
	require.Greater(t, m.revision, previous)
	require.True(t, m.updating)
}

func TestTokenizerEditorSwitchingViewsDoesNotRecompute(t *testing.T) {
	m := testTokenizerEditor()
	m.insert("ab")
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{{ID: 1, EndByte: 1}, {ID: 2, EndByte: 2}}})
	m.focus, m.selected = tokenizerFocusOptions, 1
	revision := m.revision
	for i := 0; i < 9; i++ {
		require.Nil(t, tokenizerEditorKey(m, tea.KeyRight))
		require.Equal(t, (i+1)%3, m.tab)
		require.Equal(t, 1, m.selected)
		require.Equal(t, revision, m.revision)
		require.Equal(t, "ab", m.text)
	}
}

func TestTokenizerEditorEncodingRequiresExplicitSelection(t *testing.T) {
	m := testTokenizerEditor()
	m.insert("ab")
	m.focus, m.option = tokenizerFocusOptions, 1
	m.debouncing = false
	tokenizerEditorKey(m, tea.KeyEnter)
	require.Equal(t, tokenizerModalEncoding, m.modal)
	tokenizerEditorKey(m, tea.KeyDown)
	require.Equal(t, "o200k_base", m.encoding)
	tokenizerEditorKey(m, tea.KeyEscape)
	require.Equal(t, "o200k_base", m.encoding)
	require.Equal(t, tokenizerFocusText, m.focus)
	m.focus, m.option = tokenizerFocusOptions, 1
	tokenizerEditorKey(m, tea.KeyEnter)
	require.Zero(t, m.choice, "cancel must not preserve an unconfirmed choice")
	tokenizerEditorKey(m, tea.KeyDown)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	require.Equal(t, "cl100k_base", m.encoding)
	require.True(t, m.updating)
}

func TestTokenizerEditorKeepsOnlyOneDebounce(t *testing.T) {
	m := testTokenizerEditor()
	require.NotNil(t, m.insert("a"))
	first := m.revision
	for i := 0; i < 100; i++ {
		require.Nil(t, m.insert("a"))
	}
	_, cmd := m.Update(tokenizerEditorDebounceMsg{revision: first})
	require.NotNil(t, cmd)
	require.True(t, m.debouncing)
	_, cmd = m.Update(tokenizerEditorDebounceMsg{revision: m.revision})
	require.NotNil(t, cmd)
	request := cmd().(tokenizerEditorRequestMsg)
	require.Equal(t, m.text, request.Text)
	require.False(t, m.debouncing)
}

func TestTokenizerEditorControlsDoNotConsumeOrdinaryText(t *testing.T) {
	m := testTokenizerEditor()
	for _, text := range []string{"q", "?", "r", "1", "2", "3"} {
		m.Update(tea.KeyPressMsg{Code: rune(text[0]), Text: text})
	}
	require.Equal(t, "q?r123", m.text)
	require.False(t, m.quit)
	require.Zero(t, m.modal)
	tokenizerEditorKey(m, tea.KeyTab)
	require.Equal(t, tokenizerFocusOptions, m.focus)
	m.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	require.Equal(t, 1, m.modal)
	tokenizerEditorKey(m, tea.KeyEscape)
	require.Zero(t, m.modal)
	require.Equal(t, tokenizerFocusOptions, m.focus)
}

func TestTokenizerEditorCancellationWorksInEveryState(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 12}, {10, 3}} {
		for modal := tokenizerModalNone; modal <= tokenizerModalEncoding; modal++ {
			for focus := tokenizerFocusText; focus <= tokenizerFocusResults; focus++ {
				m := testTokenizerEditor()
				m.width, m.height, m.modal, m.focus = size[0], size[1], modal, focus
				_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
				require.NotNil(t, cmd)
				require.True(t, m.quit)
				require.Equal(t, 130, m.exitCode)
				require.Empty(t, m.View().Content)
				m.Update(tea.PasteMsg{Content: "late"})
				require.Empty(t, m.text)
			}
		}
	}
}

func TestTokenizerEditorTinyTerminalPreservesDraft(t *testing.T) {
	m := testTokenizerEditor()
	m.insert("keep")
	m.Update(tea.WindowSizeMsg{Width: 20, Height: 5})
	m.Update(tea.PasteMsg{Content: "discard"})
	m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	require.Equal(t, "keep", m.text)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	require.Contains(t, m.View().Content, "keep")
}

func TestTokenizerEditorPageKeysMoveByVisibleTokens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 12}} {
		m := testTokenizerEditor()
		m.width, m.height, m.focus = size[0], size[1], tokenizerFocusResults
		m.insert(strings.Repeat("a", 100))
		tokens := make([]tokenizerPreviewToken, 100)
		for i := range tokens {
			tokens[i] = tokenizerPreviewToken{ID: uint32(i), EndByte: uint32(i + 1)}
		}
		m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
		for tab := 0; tab < 3; tab++ {
			m.tab, m.selected = tab, 0
			_, visible := m.resultWindow(m.styles(), m.viewWidth())
			require.Greater(t, visible, m.resultRows())
			tokenizerEditorKey(m, tea.KeyPgDown)
			require.Equal(t, visible, m.selected)
			tokenizerEditorKey(m, tea.KeyEnd)
			step := m.previousPageSize()
			require.Greater(t, step, m.resultRows())
			tokenizerEditorKey(m, tea.KeyPgUp)
			require.Equal(t, len(tokens)-1-step, m.selected)
		}
	}
}

func TestTokenizerEditorPickerNavigation(t *testing.T) {
	m := testTokenizerEditor()
	m.insert("one\ntwo")
	m.cursor = 1
	tokenizerEditorKey(m, tea.KeyDown)
	require.Equal(t, tokenizerFocusText, m.focus)
	require.Equal(t, len("one\nt"), m.cursor)
	tokenizerEditorKey(m, tea.KeyEnd)
	tokenizerEditorKey(m, tea.KeyDown)
	require.Equal(t, tokenizerFocusOptions, m.focus)
	require.Zero(t, m.option)
	tokenizerEditorKey(m, tea.KeyDown)
	require.Equal(t, 1, m.option)
	tokenizerEditorKey(m, tea.KeyDown)
	require.Equal(t, tokenizerFocusResults, m.focus)
	tokenizerEditorKey(m, tea.KeyUp)
	require.Equal(t, tokenizerFocusOptions, m.focus)
	require.Equal(t, 1, m.option)
	tokenizerEditorKey(m, tea.KeyUp)
	tokenizerEditorKey(m, tea.KeyUp)
	require.Equal(t, tokenizerFocusText, m.focus)
	for _, focus := range []int{tokenizerFocusOptions, tokenizerFocusResults, tokenizerFocusText} {
		tokenizerEditorKey(m, tea.KeyTab)
		require.Equal(t, focus, m.focus)
	}
	for _, focus := range []int{tokenizerFocusResults, tokenizerFocusOptions, tokenizerFocusText} {
		m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
		require.Equal(t, focus, m.focus)
	}
	require.Equal(t, "one\ntwo", m.text)
}

func TestTokenizerEditorResultsSeparateVerticalAndTokenNavigation(t *testing.T) {
	for tab := 0; tab < 3; tab++ {
		for _, selected := range []int{0, 3, 7} {
			m := testTokenizerEditor()
			m.insert("abcdefgh")
			tokens := make([]tokenizerPreviewToken, len(m.text))
			for index := range tokens {
				tokens[index] = tokenizerPreviewToken{ID: uint32(index), EndByte: uint32(index + 1)}
			}
			m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
			m.focus, m.tab, m.selected = tokenizerFocusResults, tab, selected
			cursor, revision := m.cursor, m.revision
			require.Nil(t, tokenizerEditorKey(m, tea.KeyDown))
			require.Equal(t, tokenizerFocusResults, m.focus, "Down stays at the bottom section")
			require.Equal(t, selected, m.selected, "vertical keys must not select tokens")
			require.Nil(t, tokenizerEditorKey(m, tea.KeyUp))
			require.Equal(t, tokenizerFocusOptions, m.focus, "Up returns directly to settings from every token")
			require.Equal(t, 1, m.option)
			require.Equal(t, selected, m.selected)
			require.Nil(t, tokenizerEditorKey(m, tea.KeyDown))
			require.Equal(t, tokenizerFocusResults, m.focus)
			require.Equal(t, selected, m.selected, "returning must preserve the selected token")
			require.Nil(t, tokenizerEditorKey(m, tea.KeyLeft))
			require.Equal(t, max(0, selected-1), m.selected)
			require.Nil(t, tokenizerEditorKey(m, tea.KeyRight))
			require.Equal(t, min(7, max(0, selected-1)+1), m.selected)
			require.Equal(t, tokenizerFocusResults, m.focus)
			require.Equal(t, cursor, m.cursor)
			require.Equal(t, revision, m.revision)
			require.Equal(t, "abcdefgh", m.text)
		}
	}
}

func TestTokenizerEditorDownFromLastLineEntersOptions(t *testing.T) {
	for _, test := range []struct {
		name, text   string
		cursor, next int
		focus        int
	}{
		{"empty", "", 0, 0, tokenizerFocusOptions},
		{"single line start", "abc", 0, 0, tokenizerFocusOptions},
		{"single line middle", "abc", 1, 1, tokenizerFocusOptions},
		{"single line end", "abc", 3, 3, tokenizerFocusOptions},
		{"earlier line middle", "one\ntwo", 1, 5, tokenizerFocusText},
		{"earlier line end", "one\ntwo", 3, 7, tokenizerFocusText},
		{"last line start", "one\ntwo", 4, 4, tokenizerFocusOptions},
		{"last line middle", "one\ntwo", 5, 5, tokenizerFocusOptions},
		{"last line end", "one\ntwo", 7, 7, tokenizerFocusOptions},
		{"before trailing newline", "one\n", 1, 4, tokenizerFocusText},
		{"trailing empty line", "one\n", 4, 4, tokenizerFocusOptions},
		{"CRLF earlier line", "one\r\ntwo", 3, 8, tokenizerFocusText},
		{"CRLF trailing empty line", "one\r\n", 5, 5, tokenizerFocusOptions},
		{"Unicode earlier line", "é👩‍💻\nαβ", len("é"), len("é👩‍💻\nα"), tokenizerFocusText},
		{"Unicode last line", "é👩‍💻\nαβ", len("é👩‍💻\nα"), len("é👩‍💻\nα"), tokenizerFocusOptions},
		{"combining last line", "a\ne\u0301z", len("a\ne\u0301"), len("a\ne\u0301"), tokenizerFocusOptions},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := testTokenizerEditor()
			m.insert(test.text)
			tokens := make([]tokenizerPreviewToken, len(test.text))
			for index := range tokens {
				tokens[index] = tokenizerPreviewToken{ID: uint32(index), EndByte: uint32(index + 1)}
			}
			if len(tokens) > 0 {
				m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
			}
			m.cursor, m.option = test.cursor, 1
			m.selectCursorToken()
			revision, selected := m.revision, m.selected
			beforeTokens := m.tokens

			require.Nil(t, tokenizerEditorKey(m, tea.KeyDown), "navigation must not start tokenization")
			require.Equal(t, test.focus, m.focus)
			require.Equal(t, test.next, m.cursor)
			require.Contains(t, m.boundaries, m.cursor)
			require.Equal(t, test.text, m.text)
			require.Equal(t, revision, m.revision)
			require.Equal(t, beforeTokens, m.tokens)
			if test.focus == tokenizerFocusOptions {
				require.Zero(t, m.option)
				require.Equal(t, selected, m.selected, "leaving Text preserves its selected token")
			} else {
				require.Equal(t, min(test.next, len(tokens)-1), m.selected, "vertical movement keeps the caret-linked token")
			}
		})
	}
}

func TestTokenizerEditorViewChooserPreservesResultAndSelection(t *testing.T) {
	m := tokenizerEditorExample()
	m.focus, m.option = tokenizerFocusOptions, 0
	source, cursor, revision, selected := m.text, m.cursor, m.revision, m.selected
	tokens := append([]tokenizerPreviewToken(nil), m.tokens...)
	tokenizerEditorKey(m, tea.KeyEnter)
	require.Equal(t, tokenizerModalView, m.modal)
	tokenizerEditorKey(m, tea.KeyDown)
	require.Zero(t, m.tab)
	tokenizerEditorKey(m, tea.KeyLeft)
	require.Zero(t, m.tab)
	require.Equal(t, tokenizerFocusOptions, m.focus)
	tokenizerEditorKey(m, tea.KeyEnter)
	tokenizerEditorKey(m, tea.KeyEnd)
	require.Nil(t, tokenizerEditorKey(m, tea.KeyEnter))
	require.Equal(t, 2, m.tab)
	require.Equal(t, source, m.text)
	require.Equal(t, cursor, m.cursor)
	require.Equal(t, revision, m.revision)
	require.Equal(t, selected, m.selected)
	require.Equal(t, tokens, m.tokens)
	tokenizerEditorKey(m, tea.KeyTab)
	tokenizerEditorKey(m, tea.KeyLeft)
	require.Equal(t, selected-1, m.selected)
	require.Equal(t, 2, m.tab, "token navigation must not change views")
}

func TestTokenizerEditorCaretSelectsContainingTokenWithoutRecomputing(t *testing.T) {
	m := testTokenizerEditor()
	m.insert("abcdef")
	tokens := []tokenizerPreviewToken{{ID: 1, EndByte: 2}, {ID: 2, EndByte: 4}, {ID: 3, EndByte: 6}}
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
	require.Equal(t, 2, m.selected, "EOF selects the last token")
	revision := m.revision
	for _, step := range []struct {
		key              rune
		cursor, selected int
	}{
		{tea.KeyHome, 0, 0}, {tea.KeyRight, 1, 0}, {tea.KeyRight, 2, 1},
		{tea.KeyRight, 3, 1}, {tea.KeyRight, 4, 2}, {tea.KeyRight, 5, 2},
		{tea.KeyRight, 6, 2}, {tea.KeyLeft, 5, 2}, {tea.KeyHome, 0, 0}, {tea.KeyEnd, 6, 2},
	} {
		require.Nil(t, tokenizerEditorKey(m, step.key))
		require.Equal(t, step.cursor, m.cursor)
		require.Equal(t, step.selected, m.selected)
		require.Equal(t, revision, m.revision)
		require.Equal(t, tokens, m.tokens)
		require.Same(t, &tokens[0], &m.tokens[0])
		require.False(t, m.updating)
		require.Equal(t, "abcdef", m.text)
	}
}

func TestTokenizerEditorCaretKeepsGraphemesAndSplitUTF8Boundaries(t *testing.T) {
	m := testTokenizerEditor()
	source := "Aé👩‍💻e\u0301\r\nZ"
	m.insert(source)
	tokens := make([]tokenizerPreviewToken, len(source))
	for i := range tokens {
		tokens[i] = tokenizerPreviewToken{ID: uint32(i), EndByte: uint32(i + 1)}
	}
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
	positions := []int{len("Aé👩‍💻e\u0301\r\n"), len("Aé👩‍💻e\u0301"), len("Aé👩‍💻"), len("Aé"), 1, 0}
	for _, position := range positions {
		require.Nil(t, tokenizerEditorKey(m, tea.KeyLeft))
		require.Equal(t, position, m.cursor)
		require.Equal(t, position, m.selected, "each fragment is one byte; the caret selects the following byte")
		require.Contains(t, m.boundaries, m.cursor)
	}
	for i := len(positions) - 2; i >= 0; i-- {
		require.Nil(t, tokenizerEditorKey(m, tea.KeyRight))
		require.Equal(t, positions[i], m.cursor)
		require.Equal(t, positions[i], m.selected)
	}
	tokenizerEditorKey(m, tea.KeyRight)
	require.Equal(t, len(source), m.cursor)
	require.Equal(t, len(tokens)-1, m.selected)
	require.Equal(t, source, m.text)

	emoji := testTokenizerEditor()
	emoji.insert("😀")
	emoji.Update(tokenizerEditorResultMsg{Revision: emoji.revision, Tokens: []tokenizerPreviewToken{
		{ID: 1, EndByte: 1}, {ID: 2, EndByte: 4},
	}})
	require.Equal(t, 1, emoji.selected)
	tokenizerEditorKey(emoji, tea.KeyLeft)
	require.Zero(t, emoji.cursor)
	require.Zero(t, emoji.selected)
	tokenizerEditorKey(emoji, tea.KeyRight)
	require.Equal(t, 4, emoji.cursor)
	require.Equal(t, 1, emoji.selected, "EOF keeps the final split fragment accessible")
}

func TestTokenizerEditorCaretTracksMultilineMovement(t *testing.T) {
	m := testTokenizerEditor()
	source := "ab\r\nαβ\nxyz"
	m.insert(source)
	tokens := make([]tokenizerPreviewToken, len(source))
	for i := range tokens {
		tokens[i] = tokenizerPreviewToken{ID: uint32(i), EndByte: uint32(i + 1)}
	}
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
	for _, step := range []struct {
		key    rune
		cursor int
	}{
		{tea.KeyHome, len("ab\r\nαβ\n")}, {tea.KeyRight, len("ab\r\nαβ\nx")},
		{tea.KeyUp, len("ab\r\nα")}, {tea.KeyUp, 1},
		{tea.KeyEnd, 2}, {tea.KeyDown, len("ab\r\nαβ")},
	} {
		require.Nil(t, tokenizerEditorKey(m, step.key))
		require.Equal(t, step.cursor, m.cursor)
		require.Equal(t, step.cursor, m.selected)
		require.Contains(t, m.boundaries, m.cursor)
	}
	require.Equal(t, source, m.text)
}

func TestTokenizerEditorEnteringTextResyncsManualTokenSelection(t *testing.T) {
	for _, entry := range []struct {
		name          string
		focus, option int
		modal         int
		key           tea.KeyPressMsg
	}{
		{"Tab", tokenizerFocusResults, 0, tokenizerModalNone, tea.KeyPressMsg{Code: tea.KeyTab}},
		{"Shift+Tab", tokenizerFocusOptions, 0, tokenizerModalNone, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}},
		{"Escape", tokenizerFocusResults, 0, tokenizerModalNone, tea.KeyPressMsg{Code: tea.KeyEscape}},
		{"Up from View", tokenizerFocusOptions, 0, tokenizerModalNone, tea.KeyPressMsg{Code: tea.KeyUp}},
		{"Left from Tokenizer", tokenizerFocusOptions, 1, tokenizerModalNone, tea.KeyPressMsg{Code: tea.KeyLeft}},
		{"View cancel", tokenizerFocusOptions, 0, tokenizerModalView, tea.KeyPressMsg{Code: tea.KeyEscape}},
		{"Tokenizer Shift+Tab", tokenizerFocusOptions, 1, tokenizerModalEncoding, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}},
		{"Fast Escape", tokenizerFocusOptions, 0, tokenizerModalNone, tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt}},
	} {
		t.Run(entry.name, func(t *testing.T) {
			m := testTokenizerEditor()
			m.insert("abcdef")
			tokens := []tokenizerPreviewToken{{ID: 1, EndByte: 2}, {ID: 2, EndByte: 4}, {ID: 3, EndByte: 6}}
			m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
			revision := m.revision
			tokenizerEditorKey(m, tea.KeyTab)
			tokenizerEditorKey(m, tea.KeyTab)
			require.Equal(t, tokenizerFocusResults, m.focus)
			tokenizerEditorKey(m, tea.KeyHome)
			require.Zero(t, m.selected)
			tokenizerEditorKey(m, tea.KeyRight)
			require.Equal(t, 1, m.selected, "manual Tokens navigation remains independent")
			require.Equal(t, len(m.text), m.cursor)
			tokenizerEditorKey(m, tea.KeyEnter)
			tokenizerEditorKey(m, tea.KeyEscape)
			require.Equal(t, 1, m.selected, "closing Details keeps manual selection")
			m.focus, m.option, m.modal = entry.focus, entry.option, entry.modal
			_, command := m.Update(entry.key)
			require.Nil(t, command)
			require.Equal(t, tokenizerFocusText, m.focus)
			require.Equal(t, tokenizerModalNone, m.modal)
			require.Equal(t, 2, m.selected)
			require.Equal(t, revision, m.revision)
			require.Equal(t, tokens, m.tokens)
		})
	}
}

func TestTokenizerEditorCaretIgnoresStaleResultsAndClearsInvalidSelection(t *testing.T) {
	m := testTokenizerEditor()
	m.insert("abcdef")
	old := m.revision
	for range 3 {
		tokenizerEditorKey(m, tea.KeyLeft)
	}
	m.Update(tokenizerEditorResultMsg{Revision: old, Tokens: []tokenizerPreviewToken{
		{ID: 1, EndByte: 2}, {ID: 2, EndByte: 4}, {ID: 3, EndByte: 6},
	}})
	require.Equal(t, 1, m.selected, "an arriving result uses the current caret, not the request-time caret")
	m.insert("z")
	require.Empty(t, m.tokens)
	require.Zero(t, m.selected)
	require.Zero(t, m.tokenStart)
	require.True(t, m.updating)
	tokenizerEditorKey(m, tea.KeyHome)
	m.Update(tokenizerEditorResultMsg{Revision: old, Tokens: []tokenizerPreviewToken{{ID: 9, EndByte: 7}}})
	require.Empty(t, m.tokens)
	require.Zero(t, m.selected)
	require.True(t, m.updating)
	current := m.revision
	m.Update(tokenizerEditorResultMsg{Revision: current, Tokens: []tokenizerPreviewToken{
		{ID: 1, EndByte: 2}, {ID: 2, EndByte: 4}, {ID: 3, EndByte: 7},
	}})
	require.Zero(t, m.selected)
	tokenizerEditorKey(m, tea.KeyEnd)
	require.Equal(t, 2, m.selected)
	m.insert("x")
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Err: errors.New("private failure")})
	require.True(t, m.failed)
	require.Empty(t, m.tokens)
	require.Zero(t, m.selected)
	require.Zero(t, m.tokenStart)
	tokenizerEditorKey(m, tea.KeyHome)
	require.Zero(t, m.selected)
	tokenizerEditorKey(m, tea.KeyEnd)
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	require.Empty(t, m.text)
	require.Empty(t, m.tokens)
	require.Zero(t, m.selected)
	require.False(t, m.updating)
	require.False(t, m.failed)
	m.Update(tokenizerEditorResultMsg{Revision: current, Err: errors.New("late failure")})
	require.False(t, m.failed)
}

func TestTokenizerEditorCaretMovesAcrossMillionTokenPreview(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height = 40, 12
	m.insert(strings.Repeat("a", 1<<20))
	tokens := make([]tokenizerPreviewToken, len(m.text))
	for i := range tokens {
		tokens[i] = tokenizerPreviewToken{ID: 1, EndByte: uint32(i + 1)}
	}
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
	require.Equal(t, len(tokens)-1, m.selected)
	revision := m.revision
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	started := time.Now()
	for range 100 {
		require.Nil(t, tokenizerEditorKey(m, tea.KeyHome))
		require.Zero(t, m.selected)
		require.Nil(t, tokenizerEditorKey(m, tea.KeyEnd))
		require.Equal(t, len(tokens)-1, m.selected)
	}
	elapsed := time.Since(started)
	runtime.ReadMemStats(&after)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(8<<20), "caret changes must not copy the token sequence")
	require.Less(t, elapsed, 5*time.Second, "large caret jumps must remain responsive")
	require.Equal(t, revision, m.revision)
	require.Same(t, &tokens[0], &m.tokens[0])
	t.Logf("200 caret jumps: %s; allocated %d bytes", elapsed, after.TotalAlloc-before.TotalAlloc)
}

func TestTokenizerEditorChooserIncludesEverySupportedEncoding(t *testing.T) {
	encodings := []string{"o200k_base", "cl100k_base", "r50k_base", "p50k_base"}
	require.Equal(t, encodings[0], newTokenizerEditor().encoding)
	for index, encoding := range encodings {
		m := testTokenizerEditor()
		m.encoding = encoding
		m.focus, m.option = tokenizerFocusOptions, 1
		tokenizerEditorKey(m, tea.KeyEnter)
		require.Equal(t, index, m.choice)
		tokenizerEditorKey(m, tea.KeyEnd)
		require.Equal(t, len(encodings)-1, m.choice)
		tokenizerEditorKey(m, tea.KeyDown)
		require.Equal(t, len(encodings)-1, m.choice)
		tokenizerEditorKey(m, tea.KeyHome)
		target := (index + 1) % len(encodings)
		for range target {
			tokenizerEditorKey(m, tea.KeyDown)
		}
		revision := m.revision
		require.Equal(t, encoding, m.encoding, "highlighting alone must not apply a tokenizer")
		tokenizerEditorKey(m, tea.KeyEnter)
		require.Equal(t, encodings[target], m.encoding)
		require.Greater(t, m.revision, revision)
		require.Equal(t, tokenizerModalNone, m.modal)
	}
}

func TestTokenizerEditorUnconfirmedChoicesDoNotApplyOnTab(t *testing.T) {
	for _, option := range []int{0, 1} {
		for _, backwards := range []bool{false, true} {
			m := tokenizerEditorExample()
			m.focus, m.option = tokenizerFocusOptions, option
			revision := m.revision
			encoding := m.encoding
			tokenizerEditorKey(m, tea.KeyEnter)
			tokenizerEditorKey(m, tea.KeyDown)
			key := tea.KeyPressMsg{Code: tea.KeyTab}
			if backwards {
				key.Mod = tea.ModShift
			}
			m.Update(key)
			require.Zero(t, m.modal)
			require.Zero(t, m.tab)
			require.Equal(t, encoding, m.encoding)
			require.Equal(t, revision, m.revision)
		}
	}
}

func TestTokenizerEditorFastEscapeTypingMatchesPicker(t *testing.T) {
	for modal := tokenizerModalNone; modal <= tokenizerModalEncoding; modal++ {
		m := tokenizerEditorExample()
		m.focus, m.option, m.modal = tokenizerFocusOptions, 1, modal
		before := m.text
		encoding := m.encoding
		m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModAlt, Text: "x"})
		require.Equal(t, tokenizerFocusText, m.focus)
		require.Zero(t, m.modal)
		require.Equal(t, before+"x", m.text)
		require.Equal(t, encoding, m.encoding)
	}
}
