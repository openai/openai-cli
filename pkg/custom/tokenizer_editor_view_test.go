package custom

import (
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
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
	for _, size := range [][2]int{{80, 24}, {120, 40}, {60, 16}, {40, 12}, {41, 12}, {42, 12}} {
		for focus := tokenizerFocusText; focus <= tokenizerFocusResults; focus++ {
			t.Run(fmt.Sprintf("%dx%d/focus%d", size[0], size[1], focus), func(t *testing.T) {
				m := tokenizerEditorExample()
				m.width, m.height, m.focus = size[0], size[1], focus
				view := m.View()
				require.False(t, view.AltScreen)
				require.Contains(t, view.Content, "4 tokens")
				require.NotContains(t, view.Content, "13 bytes")
				options := "[Text]  Token IDs  Bytes"
				require.Contains(t, view.Content, "View   "+options)
				require.Contains(t, view.Content, "Token IDs")
				require.Contains(t, view.Content, "Bytes")
				require.Contains(t, view.Content, "Token 3 of 4")
				require.NotContains(t, view.Content, "ID 1917")
				require.NotContains(t, view.Content, "[6, 12)")
				require.Contains(t, view.Content, "Ctrl+C exit")
				require.Contains(t, view.Content, "Model  GPT-4 / GPT-3.5  Legacy")
				lines := strings.Split(view.Content, "\n")
				require.LessOrEqual(t, len(lines), m.viewHeight())
				for _, line := range lines {
					require.Less(t, ansi.StringWidth(line), m.width, line)
				}
			})
		}
	}
}

func TestTokenizerEditorTextPositionAppearsOnlyOnOverflow(t *testing.T) {
	for _, size := range [][2]int{{80, 20}, {40, 12}} {
		for _, overflowing := range []bool{false, true} {
			m := testTokenizerEditor()
			m.width, m.height = size[0], size[1]
			lines := m.sourceRows()
			if overflowing {
				lines++
			}
			m.insert(strings.Repeat("text\n", lines-1) + "end")
			frame := m.View().Content
			if overflowing {
				require.Contains(t, frame, fmt.Sprintf("Text · %d/%d", lines, lines))
			} else {
				require.NotContains(t, frame, "Text · ")
			}
			require.Equal(t, overflowing && size[0] == 80, strings.Contains(frame, "PgUp/PgDn"))
			require.Contains(t, frame, "Ctrl+C exit")
			require.Contains(t, frame, "↓ options")
			require.LessOrEqual(t, len(strings.Split(frame, "\n")), m.viewHeight())
			for _, row := range strings.Split(frame, "\n") {
				require.Less(t, ansi.StringWidth(row), m.width)
			}
		}
	}
}

func TestTokenizerEditorMaximumNewlineInputKeepsCaretAndFooter(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height = 40, 12
	source := strings.Repeat("\n", 1<<20)
	m.insert(source)
	require.Len(t, m.lineStarts, len(source)+1)
	frame := m.View().Content
	require.Contains(t, frame, "Text · 1048577/1048577")
	require.Contains(t, frame, "\x1b[7m \x1b[27m")
	require.Contains(t, frame, "Ctrl+C exit")
	require.Contains(t, frame, "↓ options")
	require.LessOrEqual(t, len(strings.Split(frame, "\n")), m.viewHeight())
	require.Equal(t, source, m.text)
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
			require.Contains(t, strings.Join(m.resultLines(m.styles(), m.viewWidth()), "\n"), "\x1b[4;7m")
			require.Contains(t, m.View().Content, "› Token ")
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
		require.Equal(t, "0", strings.Fields(ansi.Strip(strings.Join(m.resultLines(m.styles(), m.viewWidth()), "")))[0])
	}
	tokenizerEditorKey(m, tea.KeyEnd)
	require.Positive(t, m.tokenStart)
	start := m.tokenStart
	tokenizerEditorKey(m, tea.KeyLeft)
	require.Equal(t, start, m.tokenStart)
	tokenizerEditorKey(m, tea.KeyHome)
	require.Zero(t, m.tokenStart)
}

func TestTokenizerEditorPageUpFitsContinuationAndFirstPage(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height, m.focus, m.tab = 40, 12, tokenizerFocusResults, 1
	m.insert(strings.Repeat("a", 100))
	tokens := make([]tokenizerPreviewToken, 100)
	for i := range tokens {
		tokens[i] = tokenizerPreviewToken{ID: uint32(10 + i%90), EndByte: uint32(i + 1)}
	}
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
	assertPage := func(first, count int) {
		t.Helper()
		frame := ansi.Strip(m.View().Content)
		results := strings.Join(m.resultLines(m.styles(), m.viewWidth()), "\n")
		ids := strings.Fields(ansi.Strip(results))
		if first > 0 {
			require.Equal(t, "…", ids[0])
			ids = ids[1:]
		}
		require.Len(t, ids, count)
		for i, id := range ids {
			require.Equal(t, fmt.Sprint(tokens[first+i].ID), id)
		}
		require.Contains(t, frame, fmt.Sprintf("Token %d of 100", m.selected+1))
		require.Contains(t, results, fmt.Sprintf("\x1b[4;7m%d\x1b[24;27m", tokens[m.selected].ID))
		require.Contains(t, frame, "› Token ")
	}
	tokenizerEditorKey(m, tea.KeyEnd)
	assertPage(89, 11)
	for _, selected := range []int{88, 77} {
		before := m.selected
		tokenizerEditorKey(m, tea.KeyPgUp)
		require.Equal(t, 11, before-m.selected, "the fixed gutter leaves room for eleven two-digit IDs")
		require.Equal(t, selected, m.selected)
		assertPage(selected, 11)
	}
	tokenizerEditorKey(m, tea.KeyHome)
	assertPage(0, 11)
	for range 22 {
		tokenizerEditorKey(m, tea.KeyRight)
	}
	tokenizerEditorKey(m, tea.KeyPgUp)
	require.Equal(t, 11, m.selected)
	assertPage(11, 11)
	tokenizerEditorKey(m, tea.KeyPgUp)
	require.Zero(t, m.selected, "first and continuation pages share the same fixed gutter")
	assertPage(0, 11)
	tokenizerEditorKey(m, tea.KeyPgDown)
	require.Equal(t, 11, m.selected)
	tokenizerEditorKey(m, tea.KeyPgUp)
	require.Zero(t, m.selected)
	assertPage(0, 11)
	tokenizerEditorKey(m, tea.KeyPgUp)
	require.Zero(t, m.selected)
	assertPage(0, 11)
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
		view = strings.NewReplacer("\x1b[7m", "", "\x1b[27m", "", "\x1b[4;7m", "", "\x1b[24;27m", "").Replace(view)
		require.NotContains(t, view, "\x1b")
		require.NotContains(t, view, "\x00")
		require.NotContains(t, view, "\r")
		require.NotContains(t, view, "\u202e")
		require.NotContains(t, view, "�")
	}
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
				{tokenizerFocusText, 0, "\x1b[7m \x1b[27m", "↓ options"},
				{tokenizerFocusOptions, 0, "› View", "←→ view"},
				{tokenizerFocusOptions, 1, "› Model", "Enter select"},
				{tokenizerFocusResults, 0, "› Token ", "Enter details"},
			} {
				m := tokenizerEditorExample()
				m.width, m.height, m.color, m.dark = size[0], size[1], false, dark
				m.focus, m.option = state.focus, state.option
				view := m.View().Content
				for _, rendition := range regexp.MustCompile(`\x1b\[[0-9;:]*m`).FindAllString(view, -1) {
					require.Contains(t, []string{"\x1b[7m", "\x1b[27m", "\x1b[4;7m", "\x1b[24;27m"}, rendition, "NO_COLOR retains functional monochrome cues")
				}
				options := "[Text]  Token IDs  Bytes"
				require.Contains(t, view, "View   "+options)
				require.Contains(t, view, "Model  GPT-4 / GPT-3.5  Legacy")
				if m.focus == tokenizerFocusResults {
					require.Contains(t, view, "› Token ")
				} else {
					require.NotContains(t, view, "› Token ")
				}
				require.Contains(t, view, state.marker)
				require.Contains(t, view, state.hint)
				require.Contains(t, view, "Ctrl+C exit")
			}
		}
	}
}

func TestTokenizerEditorFocusFillStaysInActiveRegion(t *testing.T) {
	background := regexp.MustCompile(`\x1b\[[0-9;]*48;2;[0-9;]+m([^\x1b]*)`)
	for _, size := range [][2]int{{80, 24}, {40, 12}} {
		for _, dark := range []bool{false, true} {
			for _, state := range []struct{ focus, option int }{
				{tokenizerFocusText, 0}, {tokenizerFocusOptions, 0},
				{tokenizerFocusOptions, 1}, {tokenizerFocusResults, 0},
			} {
				for tab := 0; tab < 3; tab++ {
					m := tokenizerEditorExample()
					m.width, m.height, m.color, m.dark = size[0], size[1], true, dark
					m.focus, m.option, m.tab = state.focus, state.option, tab
					rows := strings.Split(m.View().Content, "\n")
					for _, row := range rows {
						plain := ansi.Strip(row)
						fills := background.FindAllStringSubmatch(row, -1)
						switch {
						case strings.Contains(plain, "View   "):
							if state.focus == tokenizerFocusOptions && state.option == 0 {
								require.Len(t, fills, 1)
								require.Equal(t, []string{"[Text]", "[Token IDs]", "[Bytes]"}[tab], fills[0][1])
							} else {
								require.Empty(t, fills)
							}
						case strings.Contains(plain, "Model  GPT-4 / GPT-3.5  Legacy"):
							if state.focus == tokenizerFocusOptions && state.option == 1 {
								require.Len(t, fills, 1)
								require.Equal(t, "GPT-4 / GPT-3.5  Legacy", fills[0][1])
							} else {
								require.Empty(t, fills)
							}
						case strings.Contains(plain, "Token 3 of 4"):
							require.Empty(t, fills, "selection caption must not form a second highlighted row")
						}
						if !m.roomy() && state.focus != tokenizerFocusText && strings.HasPrefix(strings.TrimSpace(plain), "Text ") {
							focusColor := "38;2;49;89;188"
							if dark {
								focusColor = "38;2;138;168;255"
							}
							require.NotContains(t, row, focusColor, "inactive compact Text must not retain focus color")
						}
					}
					chips := strings.Join(m.resultLines(m.styles(), m.viewWidth()), "\n")
					focusFill := "48;2;49;89;188"
					if dark {
						focusFill = "48;2;138;168;255"
					}
					var fills [][]string
					for _, fill := range background.FindAllStringSubmatch(chips, -1) {
						if strings.Contains(fill[0], focusFill) {
							fills = append(fills, fill)
						}
					}
					if state.focus == tokenizerFocusResults || state.focus == tokenizerFocusText {
						require.Len(t, fills, 1, "the selected token receives the contrasting fill")
						require.Contains(t, chips, "\x1b[1;4m")
					} else {
						require.Empty(t, fills)
					}
					require.NotContains(t, ansi.Strip(chips), "›", "keyboard focus belongs in the heading")
				}
			}
		}
	}
}

func TestTokenizerEditorRowsShareAlignedGuttersAndValues(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 12}} {
		for focus := tokenizerFocusText; focus <= tokenizerFocusResults; focus++ {
			m := tokenizerEditorExample()
			m.width, m.height, m.focus, m.tab = size[0], size[1], focus, 1
			rows := strings.Split(ansi.Strip(m.View().Content), "\n")
			column := func(text string) int {
				for _, row := range rows {
					if index := strings.Index(row, text); index >= 0 {
						return ansi.StringWidth(row[:index])
					}
				}
				t.Fatalf("missing aligned content %q in %q", text, rows)
				return -1
			}
			gutter := column("4 tokens")
			for _, label := range []string{"View", "Model  GPT-4 / GPT-3.5  Legacy", "Token 3 of 4", "9906"} {
				require.Equal(t, gutter, column(label), label)
			}
			options := "Text  [Token IDs]  Bytes"
			require.Equal(t, column(options), column("GPT-4 / GPT-3.5  Legacy"))
			consecutiveBlank := false
			for _, row := range rows {
				blank := strings.TrimSpace(row) == ""
				require.False(t, blank && consecutiveBlank, "blocks should have one separating blank row")
				consecutiveBlank = blank
			}
			require.Contains(t, rows[len(rows)-1], "Ctrl+C exit")
		}
	}
}

func TestTokenizerEditorTokenSpacingDoesNotReflowOnFocusOrSelection(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 12}} {
		m := tokenizerEditorExample()
		m.width, m.height, m.tab = size[0], size[1], 1
		var previous string
		for focus := tokenizerFocusText; focus <= tokenizerFocusResults; focus++ {
			m.focus = focus
			for selected := range m.tokens {
				m.selected = selected
				rows, count := m.resultWindow(m.styles(), m.viewWidth())
				require.Equal(t, len(m.tokens), count)
				view := ansi.Strip(strings.Join(rows, "\n"))
				require.NotContains(t, view, "][")
				require.NotContains(t, view, "]›[")
				if previous != "" {
					require.Equal(t, previous, view, "styling must not change token content or geometry")
				}
				previous = view
				for _, row := range rows {
					require.LessOrEqual(t, ansi.StringWidth(row), m.viewWidth())
				}
			}
		}
	}
}

func TestTokenizerEditorEmptyResultsRetainFocusMarker(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 12}} {
		for _, state := range []string{"empty", "updating", "failed"} {
			m := testTokenizerEditor()
			m.width, m.height, m.focus = size[0], size[1], tokenizerFocusResults
			if state != "empty" {
				m.insert("pending")
			}
			if state == "failed" {
				m.Update(tokenizerEditorResultMsg{Revision: m.revision, Err: errors.New("synthetic failure")})
			}
			view := m.View().Content
			require.Contains(t, view, "› Tokens")
			require.NotContains(t, view, "›[")
			require.Contains(t, view, "Ctrl+C exit")
		}
	}
}

func TestTokenizerEditorSelectedSegmentDoesNotClaimKeyboardFocus(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {80, 24}} {
		for _, color := range []bool{false, true} {
			m := tokenizerEditorExample()
			m.width, m.height, m.color = size[0], size[1], color
			m.focus, m.tab, m.selected = tokenizerFocusText, 1, 2
			rows := strings.Join(m.resultLines(m.styles(), m.viewWidth()), "\n")
			require.Contains(t, ansi.Strip(rows), "9906 11 1917 0")
			require.NotContains(t, rows, "›", "linked selection must not claim keyboard focus")
			selected := m.styles().tokenSelected.Render("\x1b[1;4m1917\x1b[22;24m")
			if !color {
				selected = "\x1b[4;7m1917\x1b[24;27m"
			}
			require.Contains(t, rows, selected)
			m.focus = tokenizerFocusOptions
			rows = strings.Join(m.resultLines(m.styles(), m.viewWidth()), "\n")
			require.NotContains(t, rows, selected)
			require.NotContains(t, rows, "›")
			m.focus, m.updating = tokenizerFocusText, true
			require.NotContains(t, strings.Join(m.resultLines(m.styles(), m.viewWidth()), "\n"), selected)
			m.updating, m.failed = false, true
			require.NotContains(t, strings.Join(m.resultLines(m.styles(), m.viewWidth()), "\n"), selected)
		}
	}
}

func TestTokenizerEditorContinuousTextPreservesLiteralSegments(t *testing.T) {
	for _, pieces := range [][]string{
		{"Hello", ",", " world", "!"},
		{" say ", `"hi"`, " [x]", ` \path`, "  "},
		{" ", "  ", " "},
		{"é", " 日本語"},
	} {
		for _, width := range []int{40, 80} {
			for _, color := range []bool{false, true} {
				m := testTokenizerEditor()
				m.width, m.color = width, color
				source := strings.Join(pieces, "")
				m.insert(source)
				var tokens []tokenizerPreviewToken
				end := 0
				for index, piece := range pieces {
					end += len(piece)
					tokens = append(tokens, tokenizerPreviewToken{ID: uint32(index + 1), EndByte: uint32(end)})
				}
				m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
				for focus := tokenizerFocusText; focus <= tokenizerFocusResults; focus++ {
					m.focus = focus
					for selected, piece := range pieces {
						m.selected = selected
						rows, count := m.resultWindow(m.styles(), m.viewWidth())
						result := strings.Join(rows, "")
						require.Equal(t, len(pieces), count)
						require.Equal(t, "  "+source, ansi.Strip(result), "only the fixed gutter may precede the exact source")
						if focus == tokenizerFocusOptions {
							continue
						}
						selectedText := "\x1b[4;7m" + piece + "\x1b[24;27m"
						if color {
							selectedText = m.styles().tokenSelected.Render("\x1b[1;4m" + piece + "\x1b[22;24m")
							require.Contains(t, selectedText, "\x1b[1;4m"+piece+"\x1b[22;24m")
						}
						require.Contains(t, result, selectedText, "selection must cover the complete token, including spaces")
					}
				}
			}
		}
	}
}

func TestTokenizerEditorNumericSegmentsUseUnwrappedSeparation(t *testing.T) {
	m := testTokenizerEditor()
	m.focus = tokenizerFocusOptions
	m.insert("abcd")
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{
		{ID: 10, EndByte: 1}, {ID: 20, EndByte: 2}, {ID: 30, EndByte: 3}, {ID: 40, EndByte: 4},
	}})
	for tab, want := range map[int]string{1: "  10 20 30 40", 2: "  61  62  63  64"} {
		m.tab = tab
		require.Equal(t, want, strings.Join(m.resultLines(m.styles(), m.viewWidth()), ""))
	}
}

func TestTokenizerEditorSingleTokenResultsKeepFocusHeading(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height = 40, 12
	m.insert("  ")
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{{ID: 1, EndByte: 2}}})
	m.focus = tokenizerFocusResults
	view := m.View().Content
	require.Contains(t, view, "› Token 1 of 1")
	require.Contains(t, view, "\x1b[4;7m  \x1b[24;27m")
	require.Contains(t, view, "Ctrl+C exit")
	require.LessOrEqual(t, len(strings.Split(view, "\n")), m.viewHeight())
}

func TestTokenizerEditorZeroWidthTokenWindowsRemainBounded(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height, m.focus = 40, 12, tokenizerFocusResults
	m.text = strings.Repeat("\u200d", 100000)
	m.tokens = make([]tokenizerPreviewToken, 100000)
	for index := range m.tokens {
		m.tokens[index] = tokenizerPreviewToken{ID: uint32(index), EndByte: uint32((index + 1) * 3)}
	}
	m.selected = len(m.tokens) - 1
	m.keepSelectionVisible()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	rows, count := m.resultWindow(m.styles(), m.viewWidth())
	runtime.ReadMemStats(&after)
	require.Equal(t, 5, count, "zero-width fragments must occupy visible escape cells")
	require.Contains(t, strings.Join(rows, ""), `\u200d`)
	require.Contains(t, strings.Join(rows, ""), "\x1b[4;7m")
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(256<<10))
	for _, row := range rows {
		require.LessOrEqual(t, tokenizerSourceWidth(row), m.viewWidth())
	}
}

func TestTokenizerEditorContinuousEmojiUsesPainterWidths(t *testing.T) {
	for _, cluster := range []string{"👩‍💻", "👨‍👩‍👧‍👦", "🇯🇵"} {
		for _, color := range []bool{false, true} {
			m := testTokenizerEditor()
			m.width, m.height, m.focus = 40, 12, tokenizerFocusResults
			m.color = color
			m.insert(strings.Repeat(cluster, 8))
			tokens := make([]tokenizerPreviewToken, 8)
			for index := range tokens {
				tokens[index] = tokenizerPreviewToken{ID: uint32(index), EndByte: uint32((index + 1) * len(cluster))}
			}
			m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
			for selected := range tokens {
				m.selected = selected
				rows, count := m.resultWindow(m.styles(), m.viewWidth())
				require.Positive(t, count)
				for _, row := range rows {
					require.LessOrEqual(t, tokenizerSourceWidth(row), m.viewWidth())
					frame := imagePickerInlineFrame("  "+row, m.width)
					cue := "\x1b[4;7m" + cluster + "\x1b[24;27m"
					if color {
						cue = "\x1b[1;4m" + cluster + "\x1b[22;24m"
					}
					require.Contains(t, frame, cue, "ANSI must surround the complete grapheme")
				}
			}
		}
	}
}

func TestTokenizerEditorCaretDoesNotShiftSource(t *testing.T) {
	for _, source := range []string{"hello", "e\u0301日本👩‍💻", "a\tb"} {
		m := testTokenizerEditor()
		m.insert(source)
		for _, cursor := range m.boundaries {
			m.cursor = cursor
			line := m.editorLine(0, len(source), cursor, 76)
			want := tokenizerSourceDisplay(source)
			if cursor == len(source) {
				want += " "
			}
			require.Equal(t, want, ansi.Strip(line), "caret must not insert a cell between graphemes")
			require.NotContains(t, line, "▏")
			require.Equal(t, 1, strings.Count(line, "\x1b[7m"))
			require.Equal(t, 1, strings.Count(line, "\x1b[27m"))
			require.Equal(t, source, m.text)
		}
	}
}

func TestTokenizerEditorCaretSurvivesASCIIProfile(t *testing.T) {
	for _, source := range []string{"", "hello", "👩‍💻", "\n"} {
		for _, size := range [][2]int{{40, 12}, {80, 24}} {
			m := testTokenizerEditor()
			m.width, m.height = size[0], size[1]
			m.insert(source)
			for _, cursor := range m.boundaries {
				m.cursor = cursor
				var converted strings.Builder
				writer := &colorprofile.Writer{Forward: &converted, Profile: colorprofile.ASCII}
				_, err := writer.WriteString(m.View().Content)
				require.NoError(t, err)
				frame := imagePickerInlineFrame(converted.String(), m.width)
				require.Contains(t, frame, "\x1b[7m")
				require.Contains(t, frame, "\x1b[27m")
				for _, rendition := range regexp.MustCompile(`\x1b\[[0-9;:]*m`).FindAllString(frame, -1) {
					require.Contains(t, []string{"\x1b[7m", "\x1b[27m"}, rendition)
				}
			}
		}
	}
}

func TestTokenizerEditorCaretRemainsVisibleOnOversizedGrapheme(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height = 40, 12
	source := strings.Repeat("👩\u200d", 17) + "👩"
	m.insert(source)
	tokenizerEditorKey(m, tea.KeyHome)
	require.Zero(t, m.cursor)
	require.Len(t, m.boundaries, 2, "fixture contains one whole grapheme")
	var converted strings.Builder
	writer := &colorprofile.Writer{Forward: &converted, Profile: colorprofile.ASCII}
	_, err := writer.WriteString(m.View().Content)
	require.NoError(t, err)
	frame := imagePickerInlineFrame(converted.String(), m.width)
	require.Contains(t, frame, "\x1b[7m…\x1b[27m", "clipping must not hide the current caret")
	require.Equal(t, source, m.text)
	require.Zero(t, m.cursor)
	tokenizerEditorKey(m, tea.KeyRight)
	require.Equal(t, len(source), m.cursor)
	require.Contains(t, m.View().Content, "\x1b[7m \x1b[27m")
}

func TestTokenizerEditorTokenPaletteStaysStableAcrossViewsAndPages(t *testing.T) {
	background := regexp.MustCompile(`48;2;([0-9]+;[0-9]+;[0-9]+)`)
	for _, dark := range []bool{false, true} {
		m := testTokenizerEditor()
		m.color, m.dark, m.focus = true, dark, tokenizerFocusOptions
		m.insert(strings.Repeat("word ", 8))
		tokens := make([]tokenizerPreviewToken, 8)
		for i := range tokens {
			tokens[i] = tokenizerPreviewToken{ID: uint32(100 + i), EndByte: uint32((i + 1) * 5)}
		}
		m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
		var original []string
		for tab := 0; tab < 3; tab++ {
			m.tab = tab
			lines := strings.Join(m.resultLines(m.styles(), m.viewWidth()), "\n")
			var colors []string
			for _, match := range background.FindAllStringSubmatch(lines, -1) {
				colors = append(colors, match[1])
			}
			require.Len(t, colors, len(tokens))
			if tab == 0 {
				original = colors
				require.Len(t, map[string]bool{colors[0]: true, colors[1]: true, colors[2]: true, colors[3]: true, colors[4]: true, colors[5]: true}, 6)
			} else {
				require.Equal(t, original, colors)
			}
			require.Equal(t, colors[0], colors[6])
			require.Equal(t, colors[1], colors[7])
			if tab != 1 { // All eight short IDs fit; text and byte groups require a viewport.
				m.width, m.height, m.selected, m.tokenStart = 40, 12, 7, 6
				lines = strings.Join(m.resultLines(m.styles(), m.viewWidth()), "\n")
				matches := background.FindAllStringSubmatch(lines, -1)
				require.Len(t, matches, 2)
				require.Equal(t, original[6], matches[0][1])
				require.Equal(t, original[7], matches[1][1])
				m.width, m.height, m.selected, m.tokenStart = 80, 24, 0, 0
			}
		}
		m.color = false
		require.NotRegexp(t, `\x1b\[[0-9;:]*m`, strings.Join(m.resultLines(m.styles(), m.viewWidth()), "\n"))
	}
}

func TestTokenizerEditorMainViewShowsModelNamesWithoutRedundantHints(t *testing.T) {
	for _, width := range []int{40, 80} {
		for _, choice := range []struct{ encoding, label, badge string }{
			{"o200k_base", "GPT-5.x & o1/o3", "Default"},
			{"cl100k_base", "GPT-4 / GPT-3.5", "Legacy"},
			{"r50k_base", "GPT-3", "Legacy"},
			{"p50k_base", "Codex", "Legacy"},
		} {
			m := testTokenizerEditor()
			m.width, m.height, m.encoding = width, 24, choice.encoding
			m.insert("he")
			m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{{ID: 1, EndByte: 2}}})
			view := ansi.Strip(m.View().Content)
			require.Contains(t, view, "Model  "+choice.label+"  "+choice.badge)
			require.Contains(t, view, "1 token")
			require.Contains(t, view, "he")
			require.Contains(t, m.View().Content, "\x1b[4;7mhe\x1b[24;27m")
			require.NotContains(t, view, choice.encoding)
			require.NotContains(t, view, "Token 1 of 1")
			require.NotContains(t, view, "F1 help")
			require.NotContains(t, view, "Enter newline")
			require.Contains(t, view, "↓ options")
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
			require.Contains(t, ansi.Strip(frame), "tail ")
			require.Contains(t, frame, "\x1b[7m \x1b[27m")
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
	require.Contains(t, frame, "\x1b[4;7m1048575\x1b[24;27m")
	require.Contains(t, frame, "› Token 1048576 of 1048576")
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
				paint := imagePickerInlineFrame(m.View().Content, m.width)
				frame := ansi.Strip(paint)
				require.Contains(t, frame, cluster+" ", "the ending grapheme and cursor cell must remain visible")
				require.Contains(t, paint, "\x1b[7m \x1b[27m")
				if m.roomy() {
					require.Equal(t, 6, strings.Count(frame, "│"))
				}
				require.Equal(t, source, m.text)
			})
		}
	}
}
