package tokenize

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
	return tokenizerEditorModifiedKey(m, code, 0)
}

func tokenizerEditorModifiedKey(m *tokenizerEditor, code rune, mod tea.KeyMod) tea.Cmd {
	_, cmd := m.Update(tea.KeyPressMsg{Code: code, Mod: mod})
	return cmd
}

func TestTokenizerEditorPreservesPastedBytes(t *testing.T) {
	m := testTokenizerEditor()
	source := "\ufeffa\r\nb\t\x00\x1b[31m e\u0301 👩‍💻\n"
	previous := m.revision
	_, cmd := m.Update(tea.PasteMsg{Content: source})
	require.Nil(t, cmd)
	require.Equal(t, source, m.text)
	require.Equal(t, len(source), m.cursor)
	require.True(t, m.updating)
	require.Equal(t, "o200k_base", m.encoding)
	require.Greater(t, m.revision, previous)
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
			tokenizerEditorModifiedKey(m, tea.KeyLeft, tea.ModShift)
			require.Equal(t, 1+len(cluster), m.cursor)
			tokenizerEditorModifiedKey(m, tea.KeyLeft, tea.ModShift)
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

func TestTokenizerEditorIncrementalIndexMatchesFreshIndex(t *testing.T) {
	for name, source := range map[string]string{
		"empty": "", "ASCII": "abcd", "line breaks": "a\r\nb\nc\r",
		"nonline separators":  "a\rb\u2028c\u0085d",
		"regional indicators": strings.Repeat("🇦", 33) + "z",
		"joined emoji":        "x👩‍💻👨‍👩‍👧‍👦y",
		"combining":           "e" + strings.Repeat("\u0301", 65) + "z",
		"conjunct":            "aक्\u200dषz",
		"large prefix":        strings.Repeat("abc\n", 4096) + "e\u0301👩‍💻\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			base := testTokenizerEditor()
			base.insert(source)
			positions := base.boundaries
			if len(positions) > 100 {
				positions = []int{positions[0], positions[len(positions)/2], positions[len(positions)-2], positions[len(positions)-1]}
			}
			check := func(m *tokenizerEditor, text string, cursor int) {
				t.Helper()
				fresh := testTokenizerEditor()
				fresh.text, fresh.cursor = text, cursor
				fresh.indexText()
				require.Equal(t, text, m.text)
				require.Equal(t, fresh.boundaries, m.boundaries)
				require.Equal(t, fresh.cursor, m.cursor)
				lines := []int{0}
				for index := range text {
					if text[index] == '\n' {
						lines = append(lines, index+1)
					}
				}
				require.Equal(t, lines, m.lineStarts)
			}
			for _, position := range positions {
				for _, added := range []string{"x", "\r", "\n", "\u0301", "\u200d", "🇧", "💻"} {
					m := testTokenizerEditor()
					m.insert(source)
					m.cursor = position
					m.insert(added)
					check(m, source[:position]+added+source[position:], position+len(added))
				}
				for _, key := range []rune{tea.KeyBackspace, tea.KeyDelete, 'u'} {
					m := testTokenizerEditor()
					m.insert(source)
					m.cursor = position
					index := m.cursorIndex()
					text, cursor := source, position
					switch {
					case key == tea.KeyBackspace && index > 0:
						cursor = m.boundaries[index-1]
						text = source[:cursor] + source[position:]
					case key == tea.KeyDelete && index+1 < len(m.boundaries):
						text = source[:position] + source[m.boundaries[index+1]:]
					case key == 'u':
						text, cursor = source[position:], 0
					}
					if key == 'u' {
						m.Update(tea.KeyPressMsg{Code: key, Mod: tea.ModCtrl})
					} else {
						tokenizerEditorKey(m, key)
					}
					check(m, text, cursor)
				}
			}
		})
	}
}

func TestTokenizerEditorTextDocumentAndPageNavigation(t *testing.T) {
	for _, size := range [][2]int{{80, 20}, {40, 12}} {
		m := testTokenizerEditor()
		m.width, m.height = size[0], size[1]
		source := "ab\r\nαβγ\n\n👩‍💻zz\nend\n"
		m.insert(source)
		tokens := make([]tokenizerPreviewToken, len(m.boundaries)-1)
		for index := range tokens {
			tokens[index] = tokenizerPreviewToken{ID: uint32(index), EndByte: uint32(m.boundaries[index+1])}
		}
		m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
		revision := m.revision
		_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModCtrl})
		require.Nil(t, cmd)
		require.Zero(t, m.cursor)
		require.Zero(t, m.selected)
		tokenizerEditorModifiedKey(m, tea.KeyRight, tea.ModShift)
		require.Nil(t, tokenizerEditorKey(m, tea.KeyPgDown))
		if size[0] == 80 {
			require.Equal(t, len("ab\r\nαβγ\n\n👩‍💻"), m.cursor)
			require.Equal(t, 9, m.selected)
		} else {
			require.Equal(t, len("ab\r\nα"), m.cursor)
			require.Equal(t, 4, m.selected)
		}
		tokenizerEditorKey(m, tea.KeyPgUp)
		require.Equal(t, 1, m.cursor)
		tokenizerEditorKey(m, tea.KeyPgUp)
		require.Equal(t, 1, m.cursor, "page clamping keeps the grapheme column")
		_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl})
		require.Nil(t, cmd)
		require.Equal(t, len(source), m.cursor)
		require.Equal(t, len(tokens)-1, m.selected)
		tokenizerEditorKey(m, tea.KeyPgDown)
		require.Equal(t, len(source), m.cursor)
		require.Equal(t, tokenizerFocusText, m.focus)
		require.Equal(t, source, m.text)
		require.Equal(t, revision, m.revision)
		tokenizerEditorKey(m, tea.KeyDown)
		require.Equal(t, tokenizerFocusOptions, m.focus)
	}
}

func TestTokenizerEditorWordNavigationUsesWholeGraphemes(t *testing.T) {
	for _, test := range []struct {
		text   string
		starts []int
	}{
		{"  hello\t👩‍💻 e\u0301clair\r\n世界  ", []int{2, len("  hello\t"), len("  hello\t👩‍💻 "), len("  hello\t👩‍💻 e\u0301clair\r\n")}},
		{"a \u0301b c", []int{0, len("a \u0301"), len("a \u0301b ")}},
	} {
		m := testTokenizerEditor()
		m.insert(test.text)
		revision := m.revision
		for index := len(test.starts) - 1; index >= 0; index-- {
			_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModCtrl})
			require.Nil(t, cmd)
			require.Equal(t, test.starts[index], m.cursor)
			require.Contains(t, m.boundaries, m.cursor)
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModCtrl})
		require.Zero(t, m.cursor)
		for _, next := range append(test.starts[1:], len(test.text)) {
			if m.cursor == 0 && test.starts[0] > 0 {
				m.Update(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl})
				require.Equal(t, test.starts[0], m.cursor)
			}
			_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl})
			require.Nil(t, cmd)
			require.Equal(t, next, m.cursor)
			require.Contains(t, m.boundaries, m.cursor)
		}
		require.Equal(t, revision, m.revision)
		require.Equal(t, test.text, m.text)
		require.Equal(t, tokenizerFocusText, m.focus)
	}
}

func TestTokenizerEditorMultilineCursorAndEnter(t *testing.T) {
	m := testTokenizerEditor()
	m.insert("ab\r\nαβ\nxyz")
	tokenizerEditorKey(m, tea.KeyHome)
	tokenizerEditorModifiedKey(m, tea.KeyRight, tea.ModShift)
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
	require.Greater(t, m.revision, old)
	require.Empty(t, m.tokens)
	require.True(t, m.updating)
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
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Err: errors.New("secret failure")})
	require.Equal(t, "private text", m.text)
	require.True(t, m.failed)
	require.NotContains(t, m.note, "secret")
	m.focus = tokenizerFocusResults
	previous := m.revision
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	require.Nil(t, cmd)
	require.Greater(t, m.revision, previous)
	require.True(t, m.updating)
	require.False(t, m.failed)
	require.Equal(t, "private text", m.text)
}

func TestTokenizerEditorSwitchingViewsDoesNotRecompute(t *testing.T) {
	m := testTokenizerEditor()
	m.insert("abc")
	tokens := []tokenizerPreviewToken{{ID: 1, EndByte: 1}, {ID: 2, EndByte: 2}, {ID: 3, EndByte: 3}}
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
	m.option = 1 // The last visit to settings ended on Model.
	tokenizerEditorKey(m, tea.KeyHome)
	tokenizerEditorModifiedKey(m, tea.KeyRight, tea.ModShift)
	require.Equal(t, 1, m.cursor)
	require.Equal(t, 1, m.selected)
	revision := m.revision
	for _, focus := range []int{tokenizerFocusText, tokenizerFocusOptions} {
		if focus == tokenizerFocusOptions {
			require.Nil(t, tokenizerEditorKey(m, tea.KeyTab))
			require.Zero(t, m.option, "Tab from Text returns to View")
		}
		for _, step := range []struct {
			key rune
			tab int
		}{
			{tea.KeyLeft, 2}, {tea.KeyRight, 0}, {tea.KeyRight, 1}, {tea.KeyRight, 2}, {tea.KeyRight, 0},
		} {
			require.Nil(t, tokenizerEditorKey(m, step.key))
			require.Equal(t, step.tab, m.tab)
			require.Equal(t, focus, m.focus)
			require.Equal(t, 1, m.selected)
			require.Equal(t, 1, m.cursor)
			require.Equal(t, revision, m.revision)
			require.Same(t, &tokens[0], &m.tokens[0])
			require.False(t, m.updating)
			require.Equal(t, "abc", m.text)
		}
	}
}

func TestTokenizerEditorEncodingRequiresExplicitSelection(t *testing.T) {
	m := testTokenizerEditor()
	m.insert("ab")
	m.focus, m.option = tokenizerFocusOptions, 1
	before := m.revision
	tokenizerEditorKey(m, tea.KeyEnter)
	require.Equal(t, tokenizerModalEncoding, m.modal)
	tokenizerEditorKey(m, tea.KeyDown)
	require.Equal(t, "o200k_base", m.encoding)
	tokenizerEditorKey(m, tea.KeyEscape)
	require.Equal(t, "o200k_base", m.encoding)
	require.Equal(t, tokenizerFocusText, m.focus)
	require.Equal(t, before, m.revision)
	m.focus, m.option = tokenizerFocusOptions, 1
	tokenizerEditorKey(m, tea.KeyEnter)
	require.Zero(t, m.choice, "cancel must not preserve an unconfirmed choice")
	tokenizerEditorKey(m, tea.KeyDown)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, cmd)
	require.Equal(t, "cl100k_base", m.encoding)
	require.True(t, m.updating)
	require.Greater(t, m.revision, before)
}

func TestTokenizerEditorEveryEditCreatesANewRevision(t *testing.T) {
	m := testTokenizerEditor()
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	require.Nil(t, cmd)
	first := m.revision
	require.Positive(t, first)
	for _, edit := range []tea.Msg{
		tea.KeyPressMsg{Code: 'b', Text: "b"},
		tea.KeyPressMsg{Code: tea.KeyBackspace},
		tea.PasteMsg{Content: "c"},
		tea.KeyPressMsg{Code: tea.KeyBackspace},
	} {
		before := m.revision
		_, cmd = m.Update(edit)
		require.Nil(t, cmd)
		require.Greater(t, m.revision, before)
		require.True(t, m.updating)
		require.Empty(t, m.tokens)
	}
	require.Equal(t, "a", m.text)
	m.Update(tokenizerEditorResultMsg{Revision: first, Tokens: []tokenizerPreviewToken{{ID: 1, EndByte: 1}}})
	require.Empty(t, m.tokens, "matching text must not accept an earlier revision")
	require.True(t, m.updating)
}

func TestTokenizerEditorControlsDoNotConsumeOrdinaryText(t *testing.T) {
	m := testTokenizerEditor()
	for _, text := range []string{"q", "?", "r", "1", "2", "3", "v", "m", "d"} {
		m.Update(tea.KeyPressMsg{Code: rune(text[0]), Text: text})
	}
	m.Update(tea.PasteMsg{Content: "vmd?"})
	require.Equal(t, "q?r123vmdvmd?", m.text)
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
	var draft strings.Builder
	var tokens []tokenizerPreviewToken
	for i := range 180 {
		fragment, id := "x", uint32(i%10)
		if i%60 >= 30 {
			fragment, id = " International", uint32(1000000000+i)
		}
		draft.WriteString(fragment)
		tokens = append(tokens, tokenizerPreviewToken{ID: id, EndByte: uint32(draft.Len())})
	}
	for _, size := range [][2]int{{133, 39}, {110, 28}, {80, 24}, {40, 12}} {
		m := testTokenizerEditor()
		m.width, m.height, m.focus = size[0], size[1], tokenizerFocusResults
		m.insert(draft.String())
		m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
		for tab := 0; tab < 3; tab++ {
			m.tab = tab
			tokenizerEditorKey(m, tea.KeyHome)
			width := m.resultWidth()
			require.Contains(t, m.footer(), "PgUp/PgDn")
			for {
				first := m.resultStart(width)
				_, visible := m.resultWindow(m.styles(), width)
				end := first + visible
				if end == len(tokens) {
					break
				}
				if visible > 1 {
					tokenizerEditorKey(m, tea.KeyRight)
					require.Equal(t, first, m.resultStart(width), "single-token inspection keeps this page")
				}
				tokenizerEditorKey(m, tea.KeyPgDown)
				require.Equal(t, end, m.selected, "select the first unseen token")
				require.Equal(t, end, m.resultStart(width), "show adjacent pages with no skipped tokens")
			}
			lastPage := m.resultStart(width)
			require.Positive(t, lastPage, "the fixture must exceed one page")
			tokenizerEditorKey(m, tea.KeyPgDown)
			require.Equal(t, lastPage, m.resultStart(width), "keep the final partial page")
			require.Equal(t, len(tokens)-1, m.selected)
			for m.tokenStart > 0 {
				first := m.tokenStart
				tokenizerEditorKey(m, tea.KeyPgUp)
				require.Less(t, m.tokenStart, first)
				require.Equal(t, m.tokenStart, m.selected)
				require.GreaterOrEqual(t, m.resultEnd(m.tokenStart, width), first, "keep the preceding token visible")
			}
			require.Equal(t, draft.String(), m.text)
		}
	}
}

func TestTokenizerEditorPageUpRestoresPreviousTokenPage(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height, m.focus = 40, 12, tokenizerFocusResults
	var draft strings.Builder
	var tokens []tokenizerPreviewToken
	for i := range 10 {
		fragment := " administration"
		if i == 2 {
			fragment = " b"
		}
		draft.WriteString(fragment)
		tokens = append(tokens, tokenizerPreviewToken{EndByte: uint32(draft.Len())})
	}
	m.insert(draft.String())
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
	checkPage := func(key rune, first, end int) {
		t.Helper()
		tokenizerEditorKey(m, key)
		require.Equal(t, first, m.selected)
		require.Equal(t, first, m.resultStart(m.resultWidth()))
		require.Equal(t, end, m.resultEnd(first, m.resultWidth()))
	}
	checkPage(tea.KeyHome, 0, 3)
	checkPage(tea.KeyPgDown, 3, 5)
	checkPage(tea.KeyPgDown, 5, 7)
	checkPage(tea.KeyPgUp, 3, 5) // Backfilling would also fit the short third token.

	checkPage(tea.KeyPgDown, 5, 7)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	tokenizerEditorKey(m, tea.KeyPgUp)
	require.Zero(t, m.selected, "the stored page no longer ends at this boundary after resizing")
	tokenizerEditorKey(m, tea.KeyEnd)
	require.Equal(t, len(tokens)-1, m.selected)
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

func TestTokenizerEditorLetterShortcutsFromOptionsAndResults(t *testing.T) {
	for _, tabs := range []int{1, 2} {
		for key, modal := range map[rune]int{'v': tokenizerModalView, 'm': tokenizerModalEncoding, 'd': tokenizerModalDetails, '?': tokenizerModalHelp} {
			m := tokenizerEditorExample()
			revision, selected := m.revision, m.selected
			for range tabs {
				tokenizerEditorKey(m, tea.KeyTab)
			}
			m.Update(tea.KeyPressMsg{Code: key, Text: string(key)})
			require.Equal(t, modal, m.modal, "shortcut %c after %d tabs", key, tabs)
			m.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
			require.Equal(t, modal, m.modal, "shortcuts only apply to the main screen")
			tokenizerEditorKey(m, tea.KeyEscape)
			require.Equal(t, tokenizerModalNone, m.modal)
			require.Equal(t, revision, m.revision)
			require.Equal(t, selected, m.selected)
		}
	}
}

func TestTokenizerEditorWorkspaceShortcuts(t *testing.T) {
	for _, focus := range []int{tokenizerFocusText, tokenizerFocusOptions, tokenizerFocusResults} {
		m := tokenizerEditorExample()
		m.focus, m.tab = focus, 2
		revision, selected := m.revision, m.selected

		tokenizerEditorKey(m, tea.KeyF2)
		require.Equal(t, tokenizerModalView, m.modal)
		require.Equal(t, m.tab, m.choice)
		tokenizerEditorKey(m, tea.KeyF4)
		require.Equal(t, tokenizerModalView, m.modal, "workspace shortcuts only act on the main screen")
		tokenizerEditorKey(m, tea.KeyUp)
		tokenizerEditorKey(m, tea.KeyEscape)
		require.Equal(t, tokenizerFocusOptions, m.focus)
		require.Zero(t, m.option)
		require.Equal(t, 2, m.tab, "cancel keeps the active view")

		m.focus = focus
		tokenizerEditorKey(m, tea.KeyF3)
		require.Equal(t, tokenizerModalEncoding, m.modal)
		require.Equal(t, m.encoding, tokenizer.SupportedEncodings()[m.choice])
		tokenizerEditorKey(m, tea.KeyHome)
		tokenizerEditorKey(m, tea.KeyEscape)
		require.Equal(t, tokenizerFocusOptions, m.focus)
		require.Equal(t, 1, m.option)
		require.Equal(t, "cl100k_base", m.encoding)

		m.focus = focus
		tokenizerEditorKey(m, tea.KeyF4)
		require.Equal(t, tokenizerModalDetails, m.modal)
		require.Contains(t, m.View().Content, "1917")
		tokenizerEditorKey(m, tea.KeyEscape)
		require.Equal(t, tokenizerModalNone, m.modal)
		require.Equal(t, tokenizerFocusResults, m.focus)
		require.Equal(t, selected, m.selected)
		require.Equal(t, revision, m.revision)
		require.Equal(t, "Hello, world!", m.text)
	}
}

func TestTokenizerEditorDetailsShortcutRequiresCurrentTokens(t *testing.T) {
	m := testTokenizerEditor()
	for _, state := range []string{"empty", "pending", "failed"} {
		switch state {
		case "pending":
			m.insert("pending")
		case "failed":
			m.Update(tokenizerEditorResultMsg{Revision: m.revision, Err: errors.New("synthetic failure")})
		}
		for _, focus := range []int{tokenizerFocusText, tokenizerFocusOptions, tokenizerFocusResults} {
			m.focus = focus
			require.Nil(t, tokenizerEditorKey(m, tea.KeyF4))
			require.Equal(t, tokenizerModalNone, m.modal, state)
			require.Equal(t, focus, m.focus, state)
			if focus != tokenizerFocusText {
				m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
				require.Equal(t, tokenizerModalNone, m.modal, state)
				require.Equal(t, focus, m.focus, state)
			}
		}
	}
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
		mod              tea.KeyMod
		cursor, selected int
	}{
		{tea.KeyHome, 0, 0, 0}, {tea.KeyRight, tea.ModShift, 1, 0}, {tea.KeyRight, tea.ModShift, 2, 1},
		{tea.KeyRight, tea.ModShift, 3, 1}, {tea.KeyRight, tea.ModShift, 4, 2}, {tea.KeyRight, tea.ModShift, 5, 2},
		{tea.KeyRight, tea.ModShift, 6, 2}, {tea.KeyLeft, tea.ModShift, 5, 2}, {tea.KeyHome, 0, 0, 0}, {tea.KeyEnd, 0, 6, 2},
	} {
		require.Nil(t, tokenizerEditorModifiedKey(m, step.key, step.mod))
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
		require.Nil(t, tokenizerEditorModifiedKey(m, tea.KeyLeft, tea.ModShift))
		require.Equal(t, position, m.cursor)
		require.Equal(t, position, m.selected, "each fragment is one byte; the caret selects the following byte")
		require.Contains(t, m.boundaries, m.cursor)
	}
	for i := len(positions) - 2; i >= 0; i-- {
		require.Nil(t, tokenizerEditorModifiedKey(m, tea.KeyRight, tea.ModShift))
		require.Equal(t, positions[i], m.cursor)
		require.Equal(t, positions[i], m.selected)
	}
	tokenizerEditorModifiedKey(m, tea.KeyRight, tea.ModShift)
	require.Equal(t, len(source), m.cursor)
	require.Equal(t, len(tokens)-1, m.selected)
	require.Equal(t, source, m.text)

	emoji := testTokenizerEditor()
	emoji.insert("😀")
	emoji.Update(tokenizerEditorResultMsg{Revision: emoji.revision, Tokens: []tokenizerPreviewToken{
		{ID: 1, EndByte: 1}, {ID: 2, EndByte: 4},
	}})
	require.Equal(t, 1, emoji.selected)
	tokenizerEditorModifiedKey(emoji, tea.KeyLeft, tea.ModShift)
	require.Zero(t, emoji.cursor)
	require.Zero(t, emoji.selected)
	tokenizerEditorModifiedKey(emoji, tea.KeyRight, tea.ModShift)
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
		mod    tea.KeyMod
		cursor int
	}{
		{tea.KeyHome, 0, len("ab\r\nαβ\n")}, {tea.KeyRight, tea.ModShift, len("ab\r\nαβ\nx")},
		{tea.KeyUp, 0, len("ab\r\nα")}, {tea.KeyUp, 0, 1},
		{tea.KeyEnd, 0, 2}, {tea.KeyDown, 0, len("ab\r\nαβ")},
	} {
		require.Nil(t, tokenizerEditorModifiedKey(m, step.key, step.mod))
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
		tokenizerEditorModifiedKey(m, tea.KeyLeft, tea.ModShift)
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
