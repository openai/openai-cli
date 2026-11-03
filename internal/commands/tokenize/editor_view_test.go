package tokenize

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
	cliterminal "github.com/openai/openai-cli/internal/terminal"
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
	for _, size := range [][2]int{{80, 24}, {96, 16}, {120, 40}, {60, 16}, {40, 12}, {41, 12}, {42, 12}} {
		for focus := tokenizerFocusText; focus <= tokenizerFocusResults; focus++ {
			t.Run(fmt.Sprintf("%dx%d/focus%d", size[0], size[1], focus), func(t *testing.T) {
				m := tokenizerEditorExample()
				m.width, m.height, m.focus = size[0], size[1], focus
				view := m.View()
				require.False(t, view.AltScreen)
				require.Contains(t, view.Content, "4 tokens")
				require.NotContains(t, view.Content, "13 bytes")
				require.Contains(t, ansi.Strip(view.Content), "View  [Text] Token IDs  Bytes")
				require.Contains(t, view.Content, "Token IDs")
				require.Contains(t, view.Content, "Bytes")
				require.Contains(t, view.Content, "Token 3 of 4")
				require.NotContains(t, view.Content, "1917", "Text view shows the token fragment")
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
				require.Contains(t, frame, fmt.Sprintf("Text %d/%d", lines, lines))
			} else {
				require.NotRegexp(t, `Text \d+/\d+`, frame)
			}
			require.Equal(t, overflowing, strings.Contains(frame, "PgUp/PgDn"))
			require.Contains(t, frame, "Ctrl+C exit")
			require.Contains(t, frame, "Tab tokens")
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
	view := m.View()
	frame := view.Content
	require.Contains(t, frame, "Text 1048577/1048577")
	require.Equal(t, " ", tokenizerCursorCell(t, view))
	require.Contains(t, frame, "Ctrl+C exit")
	require.Contains(t, frame, "Tab tokens")
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
				m.updating, m.failed = state == "updating", state == "failed"
				if m.updating || m.failed {
					m.tokens = nil
					status = ""
				}
				if m.failed {
					status = "Count unavailable"
				}
				if tab == 2 {
					if status != "" {
						status += "  "
					}
					status += "13 bytes"
				}
				require.Equal(t, status, m.countStatus(m.styles()))
				require.Equal(t, tab == 2, strings.Contains(m.View().Content, "13 bytes"))
			})
		}
	}
	m := testTokenizerEditor()
	m.insert("a")
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{{ID: 1, EndByte: 1}}})
	m.tab = 2
	require.Contains(t, m.View().Content, "1 token  1 byte")
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
			require.Contains(t, strings.Join(m.resultLines(m.styles(), m.viewWidth()), "\n"), "\x1b[7m")
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
		require.Contains(t, results, fmt.Sprintf("\x1b[7m%d\x1b[m", tokens[m.selected].ID))
		require.Contains(t, frame, "› Token ")
	}
	tokenizerEditorKey(m, tea.KeyEnd)
	assertPage(89, 11)
	for _, selected := range []int{78, 67} {
		before := m.tokenStart
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
	require.Equal(t, 1, m.selected)
	assertPage(1, 11)
	tokenizerEditorKey(m, tea.KeyPgUp)
	require.Zero(t, m.selected, "first and continuation pages share the same fixed gutter")
	assertPage(0, 11)
	tokenizerEditorKey(m, tea.KeyPgDown)
	require.Equal(t, 11, m.selected)
	assertPage(11, 11)
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
	require.Contains(t, view, "Tab edit")
	require.Contains(t, view, "Ctrl+C exit")
	require.NotContains(t, view, "PgUp/PgDn", "all tokens fit")
	require.NotContains(t, view, "details…")
}

func TestTokenizerEditorSourceInheritsTerminalBackground(t *testing.T) {
	m := tokenizerEditorExample()
	m.focus, m.color = tokenizerFocusText, true
	for _, dark := range []bool{false, true} {
		m.dark = dark
		m.cursor = len(m.text)
		view := m.View()
		require.NotNil(t, view.Cursor)
		sourceRow := strings.Split(view.Content, "\n")[view.Cursor.Y]
		require.Contains(t, ansi.Strip(sourceRow), "Hello, world!")
		_, source, found := strings.Cut(sourceRow, "Text")
		require.True(t, found)
		require.NotContains(t, source, "48;2;", "the source inherits the terminal background after the focused label")
	}
	m.insert("\n")
	view := m.View()
	rows := strings.Split(view.Content, "\n")
	for i, row := range rows {
		if strings.Contains(row, "Hello, world!") {
			require.Equal(t, i+1, view.Cursor.Y)
			require.Equal(t, " ", tokenizerCursorCell(t, view))
			require.NotContains(t, rows[i+1], "48;2;", "the empty cursor row inherits the terminal background")
			return
		}
	}
	t.Fatal("missing source row")
}

func TestTokenizerEditorRendersControlsWithoutExecutingThem(t *testing.T) {
	m := testTokenizerEditor()
	m.insert("A\x1b[31m\x00\r\nB\t\u202eC")
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{{ID: 1, EndByte: uint32(len(m.text))}}})
	for modal := tokenizerModalNone; modal <= tokenizerModalEncoding; modal++ {
		m.modal = modal
		view := m.View().Content
		view = strings.ReplaceAll(view, ansi.CursorHorizontalAbsolute(m.viewWidth()+4), "")
		view = strings.ReplaceAll(view, "\x1b[1m[Text]\x1b[m", "Text")
		view = strings.ReplaceAll(view, "\x1b[1mText ✓\x1b[m", "Text ✓")
		view = strings.NewReplacer("\x1b[7m", "", "\x1b[27m", "", "\x1b[m", "").Replace(view)
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

func TestTokenizerEditorPendingPasteOmitsIndicatorsAndStaleCounts(t *testing.T) {
	m := tokenizerEditorExample()
	m.insert(strings.Repeat("a", 100000))
	for _, width := range []int{40, 96} {
		m.width = width
		for tab := range 3 {
			m.tab = tab
			view := ansi.Strip(m.View().Content)
			for _, text := range []string{"Updating", "Calculating", "Ready", "Empty", "0 tokens", "4 tokens", "ID 1917"} {
				require.NotContains(t, view, text)
			}
			require.Equal(t, tab == 2, strings.Contains(view, "100013 bytes"))
			require.Contains(t, view, "Ctrl+C")
		}
	}
}

func TestTokenizerEditorNoColorKeepsSelectionAndNavigation(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 12}} {
		for _, dark := range []bool{false, true} {
			for _, state := range []struct {
				focus, option int
				marker, hint  string
			}{
				{tokenizerFocusText, 0, "› Text", "Tab tokens"},
				{tokenizerFocusOptions, 0, "› View", "←→ view"},
				{tokenizerFocusOptions, 1, "› Model", "Enter open"},
				{tokenizerFocusResults, 0, "› Token ", "Tab edit"},
			} {
				m := tokenizerEditorExample()
				m.width, m.height, m.color, m.dark = size[0], size[1], false, dark
				m.focus, m.option = state.focus, state.option
				rendered := m.View()
				require.Equal(t, state.focus == tokenizerFocusText, rendered.Cursor != nil)
				view := rendered.Content
				for _, rendition := range regexp.MustCompile(`\x1b\[[0-9;:]*m`).FindAllString(view, -1) {
					require.Contains(t, []string{"\x1b[7m", "\x1b[27m", "\x1b[1m", "\x1b[m"}, rendition, "NO_COLOR retains functional monochrome cues")
				}
				options := "\x1b[1m[Text]\x1b[m Token IDs  Bytes"
				require.Contains(t, view, "View  "+options)
				require.Contains(t, view, "Model  GPT-4 / GPT-3.5  Legacy")
				if m.focus == tokenizerFocusResults {
					require.Contains(t, view, "› Token ")
				} else {
					require.NotContains(t, view, "› Token ")
				}
				require.Contains(t, view, state.marker)
				require.Contains(t, view, state.hint)
				if state.focus == tokenizerFocusOptions && state.option == 0 {
					require.NotContains(t, m.footer(), "Enter")
				}
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
						viewRow := strings.Contains(plain, "View  [Text]") || strings.Contains(plain, "View   Text")
						switch {
						case viewRow, strings.Contains(plain, "Model  GPT-4 / GPT-3.5  Legacy"):
							selected := state.focus == tokenizerFocusOptions && ((state.option == 0) == viewRow)
							if selected {
								fillColor := "48;2;233;239;254"
								if dark {
									fillColor = "48;2;52;61;88"
								}
								var paint strings.Builder
								for _, fill := range fills {
									require.Contains(t, fill[0], fillColor)
									paint.WriteString(fill[1])
								}
								require.True(t, strings.HasPrefix(paint.String(), "› "+[]string{"View", "Model"}[state.option]))
								require.Equal(t, m.viewWidth(), ansi.StringWidth(paint.String()), "focus fills the complete settings row")
							} else {
								require.Empty(t, fills)
							}
						case strings.Contains(plain, "Token 3 of 4"):
							for _, fill := range fills {
								if strings.Contains(fill[1], "Token 3 of 4") {
									require.Equal(t, tokenizerFocusResults, state.focus, "only the focused results label receives the selection fill")
								}
							}
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
					label := m.tokenSegment(m.selected, m.viewWidth())
					require.Equal(t, 1, strings.Count(chips, m.styles().tokenSelected.Render(label)))
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
			gutter := column("View   Text")
			for _, label := range []string{"Text   Hello, world!", "Model  GPT-4 / GPT-3.5  Legacy", "Token 3 of 4"} {
				require.Equal(t, gutter, column(label), label)
			}
			options := "Text [Token IDs] Bytes"
			require.Equal(t, column(options), column("GPT-4 / GPT-3.5  Legacy"))
			require.Equal(t, column(options), column("Hello, world!"))
			require.Contains(t, strings.Join(rows, "\n"), "Token 3 of 4  9906 11 1917 0")
			require.Greater(t, column("4 tokens"), column("Tokenizer"))
		}
	}
}

func TestTokenizerEditorSettingsFocusStopsAtStudioDivider(t *testing.T) {
	for _, theme := range []struct {
		name, blue, muted, fill string
	}{
		{"dark", "138;168;255", "164;172;194", "52;61;88"},
		{"light", "49;89;188", "101;112;135", "233;239;254"},
		{"monochrome", "", "", ""},
	} {
		t.Run(theme.name, func(t *testing.T) {
			m := tokenizerEditorExample()
			m.width, m.height, m.tab, m.focus = 96, 16, 1, tokenizerFocusOptions
			m.color, m.dark = theme.name != "monochrome", theme.name == "dark"
			for option, value := range []string{"Token IDs", "GPT-4 / GPT-3.5"} {
				m.option = option
				rows := m.settingsRows(m.styles(), m.sourceWidth())
				row := rows[option]
				require.Equal(t, m.sourceWidth(), ansi.StringWidth(row))
				require.Contains(t, ansi.Strip(row), "› "+[]string{"View", "Model"}[option])
				require.Contains(t, ansi.Strip(row), value)
				require.NotContains(t, rows[1-option], "48;2;")
				require.Contains(t, m.View().Content, row+" "+ansi.CursorHorizontalAbsolute(m.sourceWidth()+4),
					"the complete setting precedes the unpainted divider gutter")
				if !m.color {
					require.Equal(t, ansi.Strip(rows[1]), rows[1])
					continue
				}
				highlight := "\x1b[1;38;2;" + theme.blue + ";48;2;" + theme.fill + "m"
				quiet := "\x1b[38;2;" + theme.muted + ";48;2;" + theme.fill + "m"
				if option == 0 {
					require.Contains(t, row, m.styles().focused.Render("› "))
					require.Contains(t, row, highlight+"[Token IDs]")
					require.Equal(t, 2, strings.Count(row, "38;2;"+theme.blue), "only the active choice and arrows use blue")
					require.Contains(t, row, quiet+" Text ")
					require.Contains(t, row, quiet+" Bytes ")
					require.Contains(t, row, "\x1b[38;2;"+theme.blue+";48;2;"+theme.fill+"m  ←→")
				} else {
					require.Contains(t, row, highlight+"› ")
					require.Contains(t, row, highlight+value)
					require.Contains(t, rows[0], "\x1b[38;2;"+theme.blue+"m  ←→")
					require.Contains(t, row, quiet+"Legacy")
					require.Contains(t, row, quiet+"  ›")
				}
			}
		})
	}
}

func TestTokenizerEditorActiveViewKeepsLabelColumns(t *testing.T) {
	m := testTokenizerEditor()
	bold := regexp.MustCompile(`\x1b\[1(?:;[0-9;]+)?m([^\x1b]*)`)
	for _, theme := range []struct {
		color, dark     bool
		active, focused string
	}{
		{true, true, "\x1b[1;38;2;138;168;255m", "\x1b[1;38;2;138;168;255;48;2;52;61;88m"},
		{true, false, "\x1b[1;38;2;49;89;188m", "\x1b[1;38;2;49;89;188;48;2;233;239;254m"},
		{false, false, "\x1b[1m", "\x1b[1m"},
	} {
		m.color, m.dark = theme.color, theme.dark
		styles := m.styles()
		for _, state := range []struct{ focus, option int }{
			{tokenizerFocusText, 0}, {tokenizerFocusOptions, 0}, {tokenizerFocusOptions, 1}, {tokenizerFocusResults, 0},
		} {
			m.focus, m.option = state.focus, state.option
			active := theme.active
			if state.focus == tokenizerFocusOptions && state.option == 0 {
				active = theme.focused
			}
			for tab, label := range []string{"Text", "Token IDs", "Bytes"} {
				m.tab = tab
				for _, width := range []int{36, 60} {
					row := m.settingsRows(styles, width)[0]
					plain := strings.TrimSpace(strings.TrimPrefix(ansi.Strip(row), "› "))
					require.Equal(t, 1, strings.Count(plain, "["))
					require.Equal(t, 1, strings.Count(plain, "]"))
					aligned := strings.NewReplacer("[", " ", "]", " ").Replace(plain)
					require.Equal(t, "View   Text  Token IDs  Bytes   ←→", aligned)
					require.Contains(t, row, active+"["+label+"]\x1b[m")
					for _, run := range bold.FindAllStringSubmatch(row, -1) {
						require.Contains(t, []string{"› ", "[" + label + "]"}, run[1])
					}
				}
			}
		}
	}
}

func TestTokenizerEditorSettingsHighlightOnlyTheInspectedOccurrence(t *testing.T) {
	m := testTokenizerEditor()
	m.insert("f f f")
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{
		{ID: 69, EndByte: 1}, {ID: 285, EndByte: 3}, {ID: 285, EndByte: 5},
	}})
	m.focus, m.tab = tokenizerFocusOptions, 1
	for _, color := range []bool{false, true} {
		m.color = color
		for _, selected := range []int{1, 2} {
			m.selected = selected
			strip := strings.Join(m.resultLines(m.styles(), 62), "")
			marker := m.styles().tokenSelected.Render("285")
			before, after, marked := strings.Cut(strip, marker)
			require.True(t, marked)
			require.Equal(t, selected-1, strings.Count(ansi.Strip(before), "285"))
			require.Equal(t, 2-selected, strings.Count(ansi.Strip(after), "285"))
			require.NotContains(t, before+after, marker)
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
				m.Update(tokenizerInputPasteErrorMsg{message: "Paste interrupted."})
				footer := m.styles().shortcuts(m.footer())
				require.Contains(t, footer, "r retry", "retry remains available after another notice")
				require.Contains(t, m.View().Content, footer, "the complete retry footer must fit")
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
			selected := m.styles().tokenSelected.Render("1917")
			require.Contains(t, rows, selected)
			m.focus = tokenizerFocusOptions
			rows = strings.Join(m.resultLines(m.styles(), m.viewWidth()), "\n")
			require.Contains(t, rows, selected)
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
		{"a", "\u3000", "\u00a0", "b"},
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
						shown := piece
						switch piece {
						case " ", "\u00a0":
							shown = "␣"
						case "  ", "\u3000":
							shown = "␣ "
						}
						want := strings.Join(pieces[:selected], "") + shown + strings.Join(pieces[selected+1:], "")
						require.Equal(t, "  "+want, ansi.Strip(result))
						require.Equal(t, tokenizerSourceWidth(source), tokenizerSourceWidth(want))
						selectedText := m.styles().tokenSelected.Render(shown)
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
	m.insert("abcde")
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{
		{ID: 10, EndByte: 2}, {ID: 20, EndByte: 3}, {ID: 30, EndByte: 4}, {ID: 40, EndByte: 5},
	}})
	for _, tc := range []struct {
		tab   int
		color bool
		want  string
	}{
		{1, false, "  10 20 30 40"},
		{2, true, "  61 62 63 64 65"},
		{2, false, "  61 62│63│64│65"},
	} {
		m.tab, m.color = tc.tab, tc.color
		width := ansi.StringWidth(tc.want)
		rows, end := m.tokenStrip(m.styles(), width, 0)
		require.Equal(t, tc.want, ansi.Strip(rows[0]))
		require.Equal(t, len(m.tokens), end)
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
	require.Contains(t, view, "\x1b[7m␣ \x1b[m")
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
	require.Contains(t, strings.Join(rows, ""), "\x1b[7m")
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
					frame := cliterminal.InlineFrame("  "+row, m.width)
					cue := m.styles().tokenSelected.Render(cluster)
					require.Contains(t, frame, cue, "ANSI must surround the complete grapheme")
				}
			}
		}
	}
}

func tokenizerCursorCell(t *testing.T, view tea.View) string {
	t.Helper()
	require.NotNil(t, view.Cursor)
	require.Equal(t, tea.CursorBar, view.Cursor.Shape)
	require.True(t, view.Cursor.Blink)
	row := strings.Split(ansi.Strip(view.Content), "\n")[view.Cursor.Y]
	for x := 0; row != ""; {
		cell, width, n := cliterminal.Sequence(row)
		if x == view.Cursor.X {
			return cell
		}
		x += width
		row = row[n:]
	}
	t.Fatalf("cursor outside rendered text at (%d, %d)", view.Cursor.X, view.Cursor.Y)
	return ""
}

func TestTokenizerEditorCaretDoesNotShiftSource(t *testing.T) {
	for _, source := range []string{"hello", "e\u0301日本👩‍💻", "a\tb"} {
		m := testTokenizerEditor()
		m.insert(source)
		for _, cursor := range m.boundaries {
			m.cursor = cursor
			line, column := m.editorLine(0, len(source), 76, m.sourceRange(), m.styles().sourceSelected)
			want := tokenizerSourceDisplay(source)
			if cursor == len(source) {
				want += " "
			}
			require.Equal(t, want, line, "the native caret adds no characters or rendition escapes")
			require.Equal(t, tokenizerSourceWidth(tokenizerSourceControls.Replace(source[:cursor])), column)
			require.Equal(t, source, m.text)
		}
	}
}

func TestTokenizerEditorCaretSurvivesASCIIProfile(t *testing.T) {
	for _, source := range []string{"", "hello", "👩‍💻", "\n"} {
		for _, size := range [][2]int{{40, 12}, {80, 24}, {120, 30}} {
			m := testTokenizerEditor()
			m.width, m.height = size[0], size[1]
			m.insert(source)
			for _, cursor := range m.boundaries {
				m.cursor = cursor
				var converted strings.Builder
				writer := &colorprofile.Writer{Forward: &converted, Profile: colorprofile.ASCII}
				view := m.View()
				_, err := writer.WriteString(view.Content)
				require.NoError(t, err)
				frame := cliterminal.InlineFrame(converted.String(), m.width)
				want := " "
				if source == "" {
					want = "T" // First cell of the placeholder.
				} else if cursor < len(source) && source[cursor] != '\n' {
					want, _, _ = cliterminal.Sequence(source[cursor:])
				}
				require.Equal(t, want, tokenizerCursorCell(t, view))
				require.Contains(t, frame, "\x1b[1m[Text]\x1b[m")
				for _, rendition := range regexp.MustCompile(`\x1b\[[0-9;:]*m`).FindAllString(frame, -1) {
					require.Contains(t, []string{"\x1b[7m", "\x1b[27m", "\x1b[1m", "\x1b[m"}, rendition)
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
	frame := cliterminal.InlineFrame(converted.String(), m.width)
	require.Contains(t, frame, "…")
	require.Equal(t, "…", tokenizerCursorCell(t, m.View()))
	require.Equal(t, source, m.text)
	require.Zero(t, m.cursor)
	tokenizerEditorModifiedKey(m, tea.KeyRight, tea.ModShift)
	require.Equal(t, len(source), m.cursor)
	require.Equal(t, " ", tokenizerCursorCell(t, m.View()))
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
		m.selected = 7
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
			require.NotEqual(t, colors[1], colors[7], "the inspected token uses the selection fill")
			if tab != 1 { // All eight short IDs fit; text and byte groups require a viewport.
				m.width, m.height, m.selected, m.tokenStart = 40, 12, 7, 6
				lines = strings.Join(m.resultLines(m.styles(), m.viewWidth()), "\n")
				matches := background.FindAllStringSubmatch(lines, -1)
				require.Len(t, matches, 2)
				require.Equal(t, original[6], matches[0][1])
				require.Equal(t, original[7], matches[1][1])
				m.width, m.height, m.selected, m.tokenStart = 80, 24, 7, 0
			}
		}
		m.color = false
		monochrome := strings.Join(m.resultLines(m.styles(), m.viewWidth()), "\n")
		require.Equal(t, []string{"\x1b[7m", "\x1b[m"}, regexp.MustCompile(`\x1b\[[0-9;:]*m`).FindAllString(monochrome, -1))
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
			require.Contains(t, m.View().Content, "\x1b[7mhe\x1b[m")
			require.NotContains(t, view, choice.encoding)
			require.NotContains(t, view, "Token 1 of 1")
			require.NotContains(t, view, "F1 help")
			require.NotContains(t, view, "Enter newline")
			require.Contains(t, view, "Tab tokens")
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
			view := m.View()
			runtime.ReadMemStats(&after)
			frame := view.Content
			require.Contains(t, ansi.Strip(frame), "tail ")
			require.Equal(t, " ", tokenizerCursorCell(t, view))
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
					normalized := strings.Join(strings.Fields(strings.ReplaceAll(view, "│", "")), " ")
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
	require.Contains(t, frame, "\x1b[7m1048575\x1b[m")
	require.Contains(t, frame, "› Token 1048576 of 1048576")
	require.Less(t, len(frame), 4096)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(256<<10), "jumping must not format the skipped tokens")
}

func TestTokenizerEditorPanelSurvivesInlinePainter(t *testing.T) {
	for _, theme := range []string{"light", "dark", "NO_COLOR"} {
		t.Run(theme, func(t *testing.T) {
			m := testTokenizerEditor()
			m.color, m.dark = theme != "NO_COLOR", theme != "light"
			source := "Line one\r\n日本語 and e\u0301\nEmoji 👩‍💻\tend\n"
			m.insert(source)
			m.cursor = len("Line one\r\n日本語 and e\u0301")
			view := m.View().Content
			rows := strings.Split(ansi.Strip(view), "\n")
			frame := ansi.Strip(cliterminal.InlineFrame(view, m.width))
			require.Equal(t, 2*(len(rows)-2), strings.Count(frame, "│"), "every interior row must retain both borders")
			require.Equal(t, 1, strings.Count(frame, "╭"))
			require.Equal(t, 1, strings.Count(frame, "╮"))
			require.Equal(t, 1, strings.Count(frame, "╰"))
			require.Equal(t, 1, strings.Count(frame, "╯"))
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
				view := m.View()
				paint := cliterminal.InlineFrame(view.Content, m.width)
				frame := ansi.Strip(paint)
				require.Contains(t, frame, cluster+" ", "the ending grapheme and cursor cell must remain visible")
				require.Equal(t, " ", tokenizerCursorCell(t, view))
				if m.roomy() {
					require.Equal(t, 2*(len(strings.Split(m.View().Content, "\n"))-2), strings.Count(frame, "│"))
				}
				require.Equal(t, source, m.text)
			})
		}
	}
}

func TestTokenizerEditorStudioAndCompactLayout(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {49, 24}, {50, 16}, {67, 24}, {68, 16}, {80, 15}, {80, 24}, {95, 16}, {96, 15}, {96, 16}, {120, 30}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := testTokenizerEditor()
			m.width, m.height = size[0], size[1]
			view := m.View().Content
			rows := strings.Split(ansi.Strip(view), "\n")
			framed := size[0] >= 50 && size[1] >= 16
			header, footer := 0, len(rows)-1
			if framed {
				header++
				footer--
				require.True(t, strings.HasPrefix(strings.TrimSpace(rows[0]), "╭"))
				require.True(t, strings.HasSuffix(strings.TrimSpace(rows[0]), "╮"))
				require.True(t, strings.HasPrefix(strings.TrimSpace(rows[len(rows)-1]), "╰"))
				require.True(t, strings.HasSuffix(strings.TrimSpace(rows[len(rows)-1]), "╯"))
				require.Less(t, len(rows), m.viewHeight(), "an empty editor uses only the rows it needs")
				for _, row := range rows {
					require.Equal(t, ansi.StringWidth(rows[0]), ansi.StringWidth(row))
					require.Less(t, ansi.StringWidth(row), m.width)
				}
			} else {
				require.NotContains(t, view, "╭")
				require.NotContains(t, view, "│")
			}
			studio := size[0] >= 96 && size[1] >= 16
			require.Contains(t, rows[header], "Tokenizer")
			if studio {
				require.NotContains(t, rows[header], "0 tokens")
				require.Contains(t, rows[header+2], "0 tokens")
				require.NotRegexp(t, `Tokens +0 tokens`, rows[header+2])
				m.focus = tokenizerFocusResults
				require.Contains(t, ansi.Strip(m.View().Content), "› 0 tokens")
				require.NotContains(t, m.footer(), "Enter details")
				require.NotContains(t, m.footer(), "←→ token")
			} else {
				require.Contains(t, rows[header], "0 tokens")
			}
			require.Contains(t, rows[header], "Tab then ")
			require.Contains(t, rows[header], "? Help")
			rule := strings.Repeat("─", m.viewWidth())
			if framed {
				require.Equal(t, "│ "+rule+" │", rows[header+1])
			} else {
				require.Equal(t, "  "+rule, rows[header+1])
			}
			require.Contains(t, rows[header+2], "› Text")
			require.Contains(t, rows[footer], "Ctrl+C exit")
			require.NotContains(t, rows[footer], "Shift+←")
			require.NotEmpty(t, strings.Trim(rows[footer-1], " │╎"), "controls follow the last content row")
			if studio {
				dividerColumn := m.sourceWidth() + 3
				for _, row := range rows[header+2 : footer] {
					require.Equal(t, '╎', []rune(row)[dividerColumn])
				}
			}
			require.Contains(t, view, "› Text")
			require.Contains(t, view, "Type or paste text")
		})
	}
}

func TestTokenizerEditorBatchPaletteForBorderAndShortcuts(t *testing.T) {
	for _, dark := range []bool{true, false} {
		m := testTokenizerEditor()
		m.color, m.dark = true, dark
		border, focus, muted := "81;86;107", "138;168;255", "164;172;194"
		if !dark {
			border, focus, muted = "186;196;216", "49;89;188", "101;112;135"
		}
		rows := strings.Split(m.View().Content, "\n")
		require.Contains(t, rows[0], "38;2;"+border)
		header := rows[1]
		for _, key := range []string{"Tab", "m", "?"} {
			require.Contains(t, header, "38;2;"+focus+"m"+key+"\x1b[")
		}
		require.NotContains(t, ansi.Strip(header), "Details")
		footer := rows[len(rows)-2]
		require.Regexp(t, "38;2;"+focus+`mCtrl\+C\x1b\[`, footer)
		require.Regexp(t, "38;2;"+muted+`mexit\x1b\[`, footer)
		require.Regexp(t, "38;2;"+muted+`mType or paste text…\x1b\[`, m.View().Content)
		for _, row := range rows {
			if strings.Contains(row, "48;2;") {
				require.Contains(t, ansi.Strip(row), "› Text")
			}
		}
	}
}

func TestTokenizerEditorHeaderShortcutsFollowKeyboardFocus(t *testing.T) {
	for _, width := range []int{40, 80, 120} {
		m := tokenizerEditorExample()
		m.width, m.height = width, 16
		for _, focus := range []int{tokenizerFocusText, tokenizerFocusOptions, tokenizerFocusResults} {
			m.focus = focus
			rows := strings.Split(ansi.Strip(m.View().Content), "\n")
			header := rows[0]
			if m.framed() {
				header = rows[1]
			}
			shortcuts := "v View  m Model  d Details  ? Help"
			if width == 40 {
				shortcuts = "? Help"
			}
			if focus == tokenizerFocusText {
				shortcuts = "Tab then " + strings.TrimPrefix(shortcuts, "v View  ")
				require.NotContains(t, header, "v View")
			} else {
				require.NotContains(t, header, "Tab then")
			}
			require.Contains(t, header, shortcuts)
			require.NotRegexp(t, `\bF[1-4]\b`, header)
		}
	}
}

func TestTokenizerEditorCompactBytesViewRetainsLongStatus(t *testing.T) {
	for _, state := range []string{"ready", "failed"} {
		t.Run(state, func(t *testing.T) {
			m := testTokenizerEditor()
			m.width, m.height, m.tab = 40, 12, 2
			m.insert(strings.Repeat("a", 100000))
			result := tokenizerEditorResultMsg{Revision: m.revision}
			status := "100000 tokens  100000 bytes"
			if state == "failed" {
				result.Err = errors.New("synthetic failure")
				status = "Count unavailable  100000 bytes"
			} else {
				result.Tokens = make([]tokenizerPreviewToken, len(m.text))
				for i := range result.Tokens {
					result.Tokens[i] = tokenizerPreviewToken{ID: 1, EndByte: uint32(i + 1)}
				}
			}
			m.Update(result)
			view := ansi.Strip(m.View().Content)
			rows := strings.Split(view, "\n")
			require.Contains(t, rows[0], "Tokenizer")
			require.Contains(t, rows[0], "Tab then ? Help")
			rule := strings.Repeat("─", m.viewWidth()-ansi.StringWidth(status)-1)
			require.Equal(t, "  "+rule+" "+status, rows[1])
			require.Contains(t, view, "Model")
			if state == "failed" {
				require.Contains(t, view, "press r")
			} else {
				require.Contains(t, view, "61")
			}
			require.Contains(t, rows[len(rows)-1], "Ctrl+C exit")
		})
	}
}
