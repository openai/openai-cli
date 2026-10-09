package custom

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestTokenizerEditorViewsShareSelectionAndExactDetails(t *testing.T) {
	m := tokenizerEditorExample()
	m.focus = tokenizerFocusResults
	for tab, expected := range []string{" world", "1917", "20 77 6f 72 6c"} {
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
		require.Contains(t, details, "Token 3 of 4")
		require.Contains(t, details, "Token ID  1917")
		require.Contains(t, details, "GPT-4 / GPT-3.5 · Legacy")
		require.NotContains(t, details, "Encoding")
		require.NotContains(t, details, "cl100k_base")
		require.Contains(t, details, "Bytes     6 · offset 6")
		require.Contains(t, details, `Text      " world"`)
		require.Contains(t, details, "Hex       20 77 6f 72 6c 64")
		tokenizerEditorKey(m, tea.KeyEscape)
		require.Equal(t, 2, m.selected)
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
			require.Contains(t, strings.Join(strings.Fields(view), " "), "Text partial UTF-8; see Hex.")
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
			if strings.HasPrefix(row, "Hex       ") {
				hexStarted = true
			}
			if hexStarted {
				recovered.WriteString(strings.ReplaceAll(row[tokenizerDetailLabelWidth:], " ", ""))
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

func TestTokenizerEditorModalEndCanScrollBack(t *testing.T) {
	for _, modal := range []int{tokenizerModalHelp, tokenizerModalDetails} {
		t.Run(fmt.Sprint(modal), func(t *testing.T) {
			m := testTokenizerEditor()
			m.width, m.height, m.focus = 40, 12, tokenizerFocusResults
			m.insert(strings.Repeat("abcdefghijklmnopqrstuvwxyz012345", 4))
			m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{{ID: 1, EndByte: 128}}})
			m.modal = modal
			rangeLabel := regexp.MustCompile(`Rows ([0-9]+)–([0-9]+) of ([0-9]+)`)
			readPage := func() (first, last, total int, body []string) {
				t.Helper()
				frame := ansi.Strip(m.View().Content)
				match := rangeLabel.FindStringSubmatch(frame)
				require.Len(t, match, 4)
				values := []*int{&first, &last, &total}
				for i, target := range values {
					value, err := strconv.Atoi(match[i+1])
					require.NoError(t, err)
					*target = value
				}
				rows := strings.Split(frame, "\n")
				require.Contains(t, rows[len(rows)-1], "Ctrl+C exit")
				body = rows[1 : len(rows)-2]
				if modal == tokenizerModalDetails {
					require.Equal(t, "  GPT-5.x & o1/o3 · Default", rows[1])
					require.Equal(t, "  ", rows[2])
					require.Equal(t, "  ", rows[len(rows)-3])
					body = rows[3 : len(rows)-3]
				}
				require.Len(t, body, last-first+1)
				return
			}
			tokenizerEditorKey(m, tea.KeyEnd)
			endFirst, endLast, total, endBody := readPage()
			require.Equal(t, total, endLast)
			require.Greater(t, endFirst, len(endBody), "fixture must cover more than two visible pages")
			tokenizerEditorKey(m, tea.KeyUp)
			upFirst, upLast, upTotal, upBody := readPage()
			require.Equal(t, endFirst-1, upFirst)
			require.Equal(t, endLast-1, upLast)
			require.Equal(t, total, upTotal)
			require.NotEqual(t, endBody, upBody)
			require.Equal(t, endBody[:len(endBody)-1], upBody[1:], "Up must move the displayed content one row")
			tokenizerEditorKey(m, tea.KeyEnd)
			tokenizerEditorKey(m, tea.KeyPgUp)
			pageFirst, pageLast, pageTotal, pageBody := readPage()
			require.Equal(t, endFirst-len(endBody), pageFirst)
			require.Equal(t, endLast-len(endBody), pageLast)
			require.Equal(t, total, pageTotal)
			require.NotEqual(t, endBody, pageBody)
			tokenizerEditorKey(m, tea.KeyHome)
			first, _, _, _ := readPage()
			require.Equal(t, 1, first)
		})
	}
}

func TestTokenizerEditorDetailsCompactLayout(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {80, 24}} {
		for _, color := range []bool{false, true} {
			for _, dark := range []bool{false, true} {
				m := tokenizerEditorExample()
				m.width, m.height, m.color, m.dark = size[0], size[1], color, dark
				m.modal = tokenizerModalDetails
				view := m.View().Content
				lines := strings.Split(ansi.Strip(view), "\n")
				require.Equal(t, []string{
					"  Token 3 of 4", "  GPT-4 / GPT-3.5 · Legacy", "  ",
					`  Text      " world"`, "  Token ID  1917", "  ",
					"  Bytes     6 · offset 6", "  Hex       20 77 6f 72 6c 64",
					"  ", "  Ctrl+C exit · Esc back",
				}, lines)
				require.NotContains(t, view, "Rows ")
				require.NotContains(t, view, "scroll")
				require.NotContains(t, view, "escaped")
				if !color {
					require.NotContains(t, view, "\x1b")
				} else {
					require.Regexp(t, `\x1b\[1mToken 3 of 4`, view)
					require.Regexp(t, `\x1b\[38;2;[0-9;]+mText      \x1b\[m" world"`, view)
				}
				for _, line := range lines {
					require.Less(t, ansi.StringWidth(line), m.width)
				}
			}
		}
	}
}

func TestTokenizerEditorDetailsModelContextAcrossViews(t *testing.T) {
	for _, model := range []struct{ encoding, label string }{
		{"o200k_base", "GPT-5.x & o1/o3 · Default"},
		{"cl100k_base", "GPT-4 / GPT-3.5 · Legacy"},
		{"r50k_base", "GPT-3 · Legacy"},
		{"p50k_base", "Codex · Legacy"},
	} {
		for tab := 0; tab < 3; tab++ {
			for _, width := range []int{40, 80} {
				t.Run(fmt.Sprintf("%s/tab%d/%d", model.encoding, tab, width), func(t *testing.T) {
					m := testTokenizerEditor()
					m.width, m.height, m.encoding, m.tab = width, 12, model.encoding, tab
					m.insert("\n")
					m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{{ID: 198, EndByte: 1}}})
					m.focus = tokenizerFocusResults
					tokenizerEditorKey(m, tea.KeyEnter)
					require.Equal(t, tokenizerModalDetails, m.modal)
					view := m.View().Content
					require.Equal(t, []string{
						"  Token 1 of 1", "  " + model.label, "  ",
						`  Text      "\n"`, "  Token ID  198", "  ",
						"  Bytes     1 · offset 0", "  Hex       0a", "  ",
						"  Ctrl+C exit · Esc back",
					}, strings.Split(ansi.Strip(view), "\n"))
					require.NotContains(t, view, "Encoding")
					require.NotContains(t, view, model.encoding)
					require.NotContains(t, view, "Rows ")
					require.NotContains(t, view, "scroll")
					require.LessOrEqual(t, len(strings.Split(view, "\n")), m.viewHeight())
					for _, row := range strings.Split(view, "\n") {
						require.Less(t, ansi.StringWidth(row), width)
					}
					tokenizerEditorKey(m, tea.KeyEscape)
					require.Equal(t, tokenizerFocusResults, m.focus)
					require.Equal(t, tab, m.tab)
					require.Equal(t, model.encoding, m.encoding)
					require.Equal(t, "\n", m.text)
				})
			}
		}
	}
}

func TestTokenizerEditorDetailsQuoteExactText(t *testing.T) {
	for _, source := range []string{` leading "quote" and 'apostrophe' \ trailing `, "\x00\t\r\n\x1b[31m\u202e ée\u0301👩‍💻"} {
		m := testTokenizerEditor()
		m.width, m.height, m.modal = 40, 12, tokenizerModalDetails
		m.insert(source)
		m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{{ID: 1, EndByte: uint32(len(source))}}})
		rows, total := m.modalRows(0, 100)
		require.Len(t, rows, total)
		var quoted strings.Builder
		for _, row := range rows {
			if strings.HasPrefix(row, "Token ID") {
				break
			}
			require.GreaterOrEqual(t, len(row), tokenizerDetailLabelWidth)
			quoted.WriteString(row[tokenizerDetailLabelWidth:])
		}
		require.Equal(t, strconv.QuoteToASCII(source), quoted.String())
		decoded, err := strconv.Unquote(quoted.String())
		require.NoError(t, err)
		require.Equal(t, source, decoded)
	}
}

func TestTokenizerEditorDetailsOverflowBoundaryAndResize(t *testing.T) {
	for _, size := range []int{18, 19} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := testTokenizerEditor()
			m.width, m.height, m.focus, m.modal = 40, 12, tokenizerFocusResults, tokenizerModalDetails
			m.insert(strings.Repeat("a", size))
			m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{{ID: 1, EndByte: uint32(size)}}})
			revision := m.revision
			view := m.View().Content
			require.Len(t, strings.Split(view, "\n"), 11)
			if size == 18 {
				require.NotContains(t, view, "Rows ")
				require.NotContains(t, view, "scroll")
			} else {
				require.Contains(t, view, "Rows 1–5 of 7")
				require.Contains(t, view, "Ctrl+C exit · Esc back · ↑↓ scroll")
			}
			tokenizerEditorKey(m, tea.KeyEnd)
			m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			view = m.View().Content
			require.NotContains(t, view, "Rows ")
			require.NotContains(t, view, "scroll")
			require.Contains(t, view, "Text      \""+m.text+"\"")
			tokenizerEditorKey(m, tea.KeyUp)
			require.Zero(t, m.scroll)
			tokenizerEditorKey(m, tea.KeyEscape)
			require.Equal(t, tokenizerModalNone, m.modal)
			require.Equal(t, tokenizerFocusResults, m.focus)
			require.Zero(t, m.selected)
			require.Equal(t, revision, m.revision)
		})
	}
}

func TestTokenizerEditorDetailsLargeTokenAllocationsStayBounded(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height, m.modal = 40, 12, tokenizerModalDetails
	m.text = strings.Repeat("a", 1<<20)
	m.tokens = []tokenizerPreviewToken{{ID: 1, EndByte: uint32(len(m.text))}}
	m.View()
	for _, scroll := range []int{0, int(^uint(0) >> 1)} {
		m.scroll = scroll
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		view := m.View().Content
		runtime.ReadMemStats(&after)
		require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(512*1024), "allocate only visible text and hexadecimal rows")
		require.Less(t, len(view), 4096)
		require.Contains(t, view, "Ctrl+C exit · Esc back · ↑↓ scroll")
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
				descriptions := []string{"Readable pieces", "Numeric token IDs", "Exact hex bytes"}
				if modal == tokenizerModalEncoding {
					title, current = "Choose model", "GPT-4 / GPT-3.5 ✓"
					choices = []string{"GPT-5.x & o1/o3", "GPT-4 / GPT-3.5", "GPT-3", "Codex"}
					descriptions = []string{"Default", "Legacy", "Legacy", "Legacy"}
				}
				for choice, label := range choices {
					m.choice = choice
					view := m.View().Content
					plain := ansi.Strip(view)
					require.Contains(t, plain, title)
					if modal == tokenizerModalEncoding {
						require.Contains(t, plain, "GPT-4o / 4.1 / 4.5 · o4-mini")
						require.Contains(t, plain, "Original GPT-4 / Turbo")
					}
					require.Contains(t, plain, "› "+label)
					require.Contains(t, plain, current, "highlighted choice must not replace the committed marker")
					require.Equal(t, 1, strings.Count(plain, "✓"))
					for _, available := range choices {
						require.Contains(t, plain, available)
					}
					descriptionColumn := -1
					for i, description := range descriptions {
						require.Contains(t, plain, description, "chooser descriptions must remain complete")
						for _, row := range strings.Split(plain, "\n") {
							primary := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(row), "›"))
							if !strings.HasPrefix(primary, choices[i]+" ") {
								continue
							}
							if index := strings.Index(row, description); index >= 0 {
								labelIndex := strings.Index(row, choices[i])
								require.GreaterOrEqual(t, labelIndex, 0)
								column := ansi.StringWidth(row[:index])
								labelWidth := 14
								if modal == tokenizerModalEncoding {
									labelWidth = 22
								}
								require.Equal(t, labelWidth, column-ansi.StringWidth(row[:labelIndex]))
								if descriptionColumn >= 0 {
									require.Equal(t, descriptionColumn, column, "description columns must align")
								}
								descriptionColumn = column
							}
						}
					}
					require.Contains(t, plain, "↑↓ move")
					require.Contains(t, plain, "Enter select")
					require.Contains(t, plain, "Esc cancel")
					require.Contains(t, plain, "Ctrl+C exit")
					require.NotContains(t, plain, "…")
					require.LessOrEqual(t, len(strings.Split(view, "\n")), m.viewHeight())
					for _, line := range strings.Split(view, "\n") {
						require.Less(t, ansi.StringWidth(line), m.width)
					}
					if !m.color {
						require.NotRegexp(t, `\x1b\[[0-9;:]*m`, view)
					} else {
						require.Regexp(t, `\x1b\[1(?:;[0-9:]+)*m`+regexp.QuoteMeta(label), view,
							"the focused chooser label must be bold, like the image picker")
					}
				}
			}
		}
	}
}
