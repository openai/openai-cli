package custom

import (
	"encoding/hex"
	"errors"
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
		for focus := tokenizerFocusText; focus <= tokenizerFocusResults; focus++ {
			t.Run(fmt.Sprintf("%dx%d/focus%d", size[0], size[1], focus), func(t *testing.T) {
				m := tokenizerEditorExample()
				m.width, m.height, m.focus = size[0], size[1], focus
				view := m.View()
				require.False(t, view.AltScreen)
				require.Contains(t, view.Content, "4 tokens")
				require.NotContains(t, view.Content, "13 bytes")
				require.Contains(t, view.Content, "View  [Text] Token IDs Bytes  ←→")
				require.Contains(t, view.Content, "Token IDs")
				require.Contains(t, view.Content, "Bytes")
				require.Contains(t, view.Content, "Token 3 of 4")
				require.NotContains(t, view.Content, "ID 1917")
				require.NotContains(t, view.Content, "[6, 12)")
				require.Contains(t, view.Content, "Ctrl+C exit")
				require.Contains(t, view.Content, "Tokenizer  cl100k_base")
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
	m.focus = tokenizerFocusResults
	for tab, expected := range []string{`" world"`, "[1917]", "20 77 6f 72 6c"} {
		m.tab = tab
		view := m.View().Content
		require.Contains(t, view, expected)
		require.Contains(t, view, "Token 3 of 4")
		require.NotContains(t, view, "ID 1917")
		require.NotContains(t, view, "[6, 12)")
		require.NotContains(t, view, "Hex:")
		tokenizerEditorKey(m, tea.KeyEnter)
		require.Equal(t, tokenizerModalDetails, m.modal)
		details := m.View().Content
		require.Contains(t, details, "Token 3 of 4 · ID 1917")
		require.Contains(t, details, "Encoding cl100k_base · bytes [6, 12)")
		require.Contains(t, details, "Text (escaped):")
		require.Contains(t, details, " world")
		require.Contains(t, details, "Hex:")
		require.Contains(t, details, "20 77 6f 72 6c 64")
		tokenizerEditorKey(m, tea.KeyEscape)
		require.Equal(t, 2, m.selected)
	}
}

func TestTokenizerEditorByteCountAppearsOnlyInBytesView(t *testing.T) {
	for _, state := range []string{"ready", "updating", "failed"} {
		for tab := 0; tab < 3; tab++ {
			t.Run(fmt.Sprintf("%s/view%d", state, tab), func(t *testing.T) {
				m := tokenizerEditorExample()
				m.tab = tab
				status := "4 tokens"
				if state != "ready" {
					m.tokens = nil
					m.updating, m.failed = state == "updating", state == "failed"
					status = "Updating…"
					if m.failed {
						status = "Count unavailable"
					}
				}
				view := m.View().Content
				if tab == 2 {
					require.Contains(t, view, status+" · 13 bytes")
				} else {
					require.Contains(t, view, status)
					require.NotContains(t, view, "13 bytes")
				}
			})
		}
	}
	m := testTokenizerEditor()
	m.insert("a")
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{{ID: 1, EndByte: 1}}})
	m.tab = 2
	require.Contains(t, m.View().Content, "1 token · 1 byte")
}

func TestTokenizerEditorCompactSelectionIsAlwaysVisible(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height, m.focus = 40, 12, tokenizerFocusResults
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
	m.focus, m.selected, m.tokenStart = tokenizerFocusResults, 3, 3
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
	m.width, m.height, m.focus, m.tab = 40, 24, tokenizerFocusResults, 1
	m.insert(strings.Repeat("a", 100))
	tokens := make([]tokenizerPreviewToken, 100)
	for i := range tokens {
		tokens[i] = tokenizerPreviewToken{ID: uint32(i), EndByte: uint32(i + 1)}
	}
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
	for next := 1; next < 5; next++ {
		tokenizerEditorKey(m, tea.KeyRight)
		require.Equal(t, next, m.selected)
		require.Zero(t, m.tokenStart)
		require.Contains(t, strings.Join(m.resultLines(m.styles(), m.viewWidth()), ""), "[0]")
	}
	tokenizerEditorKey(m, tea.KeyEnd)
	require.Positive(t, m.tokenStart)
	start := m.tokenStart
	tokenizerEditorKey(m, tea.KeyLeft)
	require.Equal(t, start, m.tokenStart)
	tokenizerEditorKey(m, tea.KeyHome)
	require.Zero(t, m.tokenStart)
}

func TestTokenizerEditorCompactResultHintsRemainComplete(t *testing.T) {
	m := tokenizerEditorExample()
	m.width, m.height, m.focus = 40, 12, tokenizerFocusResults
	view := m.View().Content
	require.Contains(t, view, "←→")
	require.Contains(t, view, "Enter details")
	require.Contains(t, view, "Ctrl+C exit")
	require.NotContains(t, view, "details…")
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
	for modal := tokenizerModalNone; modal <= tokenizerModalEncoding; modal++ {
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
	m.focus = tokenizerFocusResults
	for _, size := range [][2]int{{80, 24}, {40, 12}} {
		m.width, m.height = size[0], size[1]
		for i, expected := range []string{"f0", "9f 98 80"} {
			m.selected = i
			for tab := 0; tab < 3; tab++ {
				m.tab = tab
				view := m.View().Content
				require.Contains(t, view, "partial UTF-8")
				require.NotContains(t, view, "�")
				if tab == 0 {
					require.Contains(t, view, "0x"+strings.ReplaceAll(expected, " ", ""))
				} else if tab == 2 {
					require.Contains(t, view, expected)
				}
			}
			m.modal = tokenizerModalDetails
			view := m.View().Content
			require.Contains(t, view, expected)
			require.Contains(t, strings.Join(strings.Fields(view), " "), "Text: partial UTF-8; use the exact bytes below.")
			require.NotContains(t, view, "�")
			m.modal = tokenizerModalNone
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
	for _, size := range [][2]int{{80, 24}, {40, 12}} {
		for _, dark := range []bool{false, true} {
			for _, state := range []struct {
				focus, option int
				marker, hint  string
			}{
				{tokenizerFocusText, 0, "▏", "Tab options"},
				{tokenizerFocusOptions, 0, "› View", "↑↓ move"},
				{tokenizerFocusOptions, 1, "› Tokenizer", "Enter select"},
				{tokenizerFocusResults, 0, "› Token 3 of 4", "Enter details"},
			} {
				m := tokenizerEditorExample()
				m.width, m.height, m.color, m.dark = size[0], size[1], false, dark
				m.focus, m.option = state.focus, state.option
				view := m.View().Content
				require.NotRegexp(t, `\x1b\[[0-9;:]*m`, view)
				require.Contains(t, view, "View  [Text] Token IDs Bytes  ←→")
				require.Contains(t, view, "Tokenizer  cl100k_base")
				require.Contains(t, view, "›[")
				require.Contains(t, view, state.marker)
				require.Contains(t, view, state.hint)
				require.Contains(t, view, "Ctrl+C exit")
			}
		}
	}
}

func TestTokenizerEditorChoiceLayoutsKeepSelectionAndControlsVisible(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 12}} {
		for _, theme := range []string{"light", "dark", "NO_COLOR"} {
			for _, modal := range []int{tokenizerModalView, tokenizerModalEncoding} {
				m := tokenizerEditorExample()
				m.width, m.height, m.modal, m.tab = size[0], size[1], modal, 1
				m.color, m.dark = theme != "NO_COLOR", theme != "light"
				title, current := "Choose view", "Token IDs ✓"
				choices := []string{"Text", "Token IDs", "Bytes"}
				if modal == tokenizerModalEncoding {
					title, current = "Choose tokenizer", "cl100k_base ✓"
					choices = []string{"o200k_base", "cl100k_base"}
				}
				for choice, label := range choices {
					m.choice = choice
					view := m.View().Content
					plain := ansi.Strip(view)
					require.Contains(t, plain, title)
					require.Contains(t, plain, "› "+label)
					require.Contains(t, plain, current, "highlighted choice must not replace the committed marker")
					require.Equal(t, 1, strings.Count(plain, "✓"))
					for _, available := range choices {
						require.Contains(t, plain, available)
					}
					require.Contains(t, plain, "↑↓ choose · Enter use · Esc cancel")
					require.Contains(t, plain, "Ctrl+C exit")
					require.NotContains(t, plain, "…")
					require.LessOrEqual(t, len(strings.Split(view, "\n")), m.viewHeight())
					for _, line := range strings.Split(view, "\n") {
						require.Less(t, ansi.StringWidth(line), m.width)
					}
					if !m.color {
						require.NotRegexp(t, `\x1b\[[0-9;:]*m`, view)
					}
				}
			}
		}
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

func TestTokenizerEditorRecoveryMessagesRemainCompleteAcrossFocus(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 12}} {
		for focus := tokenizerFocusText; focus <= tokenizerFocusResults; focus++ {
			for _, failure := range []string{"invalid UTF-8", "input limit", "interrupted paste", "worker"} {
				t.Run(fmt.Sprintf("%dx%d/focus%d/%s", size[0], size[1], focus, failure), func(t *testing.T) {
					m := testTokenizerEditor()
					m.width, m.height, m.focus = size[0], size[1], focus
					m.insert("keep")
					switch failure {
					case "invalid UTF-8":
						m.insert(string([]byte{0xff}))
					case "input limit":
						m.insert(strings.Repeat("a", 1<<20))
					case "interrupted paste":
						m.Update(tokenizerInputPasteErrorMsg{message: "Paste interrupted. Your text is unchanged; paste the complete text again."})
					case "worker":
						m.Update(tokenizerEditorResultMsg{Revision: m.revision, Err: errors.New("private worker diagnostic")})
					}
					view := ansi.Strip(m.View().Content)
					normalized := strings.Join(strings.Fields(view), " ")
					require.NotEmpty(t, m.note)
					require.Contains(t, normalized, m.note)
					require.Contains(t, view, "Ctrl+C exit")
					require.NotContains(t, view, "private worker diagnostic")
					require.Equal(t, "keep", m.text)
					if failure == "worker" {
						require.Contains(t, view, "Count unavailable")
						require.Contains(t, normalized, "press r")
					}
				})
			}
		}
	}
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
	m.focus, m.tab = tokenizerFocusResults, 1
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
	require.Contains(t, frame, "Token 1048576 of 1048576")
	require.Contains(t, frame, "›[1048575]")
	require.Less(t, len(frame), 4096)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(256<<10), "jumping must not format the skipped tokens")
}

func TestTokenizerEditorSourceBordersSurviveInlinePainter(t *testing.T) {
	for _, theme := range []string{"light", "dark", "NO_COLOR"} {
		t.Run(theme, func(t *testing.T) {
			m := testTokenizerEditor()
			m.color, m.dark = theme != "NO_COLOR", theme != "light"
			source := "Line one\r\n日本語 and e\u0301\nEmoji 👩‍💻\tend\n"
			m.insert(source)
			m.cursor = len("Line one\r\n日本語 and e\u0301")
			frame := imagePickerInlineFrame(m.View().Content, m.width)
			require.Equal(t, 6, strings.Count(ansi.Strip(frame), "│"), "every source row must retain both borders")
			require.Equal(t, source, m.text)
		})
	}
}

func TestTokenizerEditorEmojiEndCursorSurvivesInlinePainter(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 12}} {
		for _, cluster := range []string{"👩‍💻", "👨‍👩‍👧‍👦", "1️⃣", "🇯🇵"} {
			t.Run(fmt.Sprintf("%d/%s", size[0], cluster), func(t *testing.T) {
				m := testTokenizerEditor()
				m.width, m.height = size[0], size[1]
				source := strings.Repeat(cluster, 200)
				m.insert(source)
				frame := ansi.Strip(imagePickerInlineFrame(m.View().Content, m.width))
				require.Contains(t, frame, cluster+"▏", "the ending grapheme and cursor must remain visible")
				if m.roomy() {
					require.Equal(t, 6, strings.Count(frame, "│"))
				}
				require.Equal(t, source, m.text)
			})
		}
	}
}
