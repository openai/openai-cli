package custom

import (
	"errors"
	"strings"
	"testing"

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

func TestTokenizerEditorViewChooserPreservesResultAndSelection(t *testing.T) {
	m := tokenizerEditorExample()
	m.focus, m.option = tokenizerFocusOptions, 0
	source, cursor, revision, selected := m.text, m.cursor, m.revision, m.selected
	tokens := append([]tokenizerPreviewToken(nil), m.tokens...)
	tokenizerEditorKey(m, tea.KeyEnter)
	require.Equal(t, tokenizerModalView, m.modal)
	tokenizerEditorKey(m, tea.KeyDown)
	require.Zero(t, m.tab)
	tokenizerEditorKey(m, tea.KeyEscape)
	require.Zero(t, m.tab)
	tokenizerEditorKey(m, tea.KeyTab)
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
