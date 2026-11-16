package tokenize

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestTokenByteCountsKeepSelectedAndNeighboringValuesAligned(t *testing.T) {
	m := testTokenizerEditor()
	m.insert("adsfasdffasdfff")
	for _, end := range []uint32{3, 6, 7, 9, 12, 15} {
		m.tokens = append(m.tokens, tokenizerPreviewToken{EndByte: end})
	}
	want := []string{"3", "3", "1", "2", "3", "3"}
	for _, theme := range []struct{ name, selected string }{
		{"dark", "\x1b[38;2;23;35;56;48;2;138;168;255m"},
		{"light", "\x1b[38;2;255;255;255;48;2;49;89;188m"},
		{"monochrome", "\x1b[7m"},
	} {
		t.Run(theme.name, func(t *testing.T) {
			m.color, m.dark = theme.name != "monochrome", theme.name == "dark"
			styles := m.styles()
			for i, length := range want {
				m.selected = i
				row := m.tokenByteCounts(styles, 49, 0, len(m.tokens))
				require.Equal(t, "  Bytes per token "+strings.Join(want, " "), ansi.Strip(row))
				before, _, found := strings.Cut(row, theme.selected+length)
				require.True(t, found)
				require.Equal(t, len("  Bytes per token ")+2*i, ansi.StringWidth(before))
				require.Equal(t, 1, strings.Count(row, theme.selected))
				wantStyled := append([]string(nil), want...)
				for j, count := range wantStyled {
					wantStyled[j] = styles.byteCounts[j].Render(count)
				}
				wantStyled[i] = styles.tokenSelected.Render(length)
				require.Equal(t, "  "+styles.muted.Render("Bytes per token")+" "+strings.Join(wantStyled, " "), row)
			}
		})
	}
}

func TestTokenizerStudioIdentifiesSelectedTrailingSpaces(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height = 120, 30
	source := "how are you im doing so well!         "
	m.insert(source)
	var tokens []tokenizerPreviewToken
	for _, end := range []uint32{3, 7, 11, 14, 20, 23, 28, 29, 38} {
		tokens = append(tokens, tokenizerPreviewToken{EndByte: end})
	}
	tokens[8].ID = 983
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
	for _, theme := range []string{"dark", "light", "monochrome"} {
		m.dark, m.color = theme == "dark", theme != "monochrome"
		s := m.styles()
		rows := m.studioResults(s, m.resultWidth())
		require.Equal(t, "how are you im doing so well!␣        ", strings.TrimPrefix(ansi.Strip(rows[0]), "  "))
		require.Contains(t, rows[0], s.tokenSelected.Render("␣        "))
		require.Contains(t, rows[1], s.tokenSelected.Render("9"))
		require.Contains(t, rows[2], s.tokenSelected.Render("9 spaces"))
		require.Equal(t, "Token 9 of 9  9 spaces", strings.TrimSpace(ansi.Strip(rows[2])))
		if m.color {
			require.Contains(t, rows[1], s.byteCounts[0].Render("3"))
			require.Contains(t, rows[1], s.byteCounts[0].Render("5"), "seventh token repeats the first color")
			require.Equal(t, 1, strings.Count(rows[1], "48;2;"), "only the selected count uses a background")
		}
	}
	m.width, m.height = 40, 12
	require.Contains(t, ansi.Strip(m.View().Content), "Token 9 of 9  9 spaces")
	m.modal = tokenizerModalDetails
	details, _ := m.modalRows(0, 20)
	require.Contains(t, details, `Text      "         "  9 spaces`)
	require.Contains(t, details, "Token ID  983")
	require.Contains(t, details, "Starts    After 29 bytes")
	require.Contains(t, details, "Size      9 bytes")
	require.Contains(t, details, "Hex       20 20 20 20 20 20 20 20 20")
	require.Equal(t, source, m.text)
}

func TestTokenizerPaletteKeepsAdjacentTokensDistinct(t *testing.T) {
	m := testTokenizerEditor()
	m.color = true
	for _, dark := range []bool{true, false} {
		m.dark = dark
		s := m.styles()
		for i, token := range s.tokens {
			require.Equal(t, token.GetForeground(), s.byteCounts[i].GetForeground())
			for _, profile := range []colorprofile.Profile{colorprofile.TrueColor, colorprofile.ANSI256, colorprofile.ANSI} {
				previous := s.tokens[(i+len(s.tokens)-1)%len(s.tokens)]
				require.NotEqual(t, profile.Convert(previous.GetBackground()), profile.Convert(token.GetBackground()), "adjacent tokens need distinct fills")
				require.NotEqual(t, profile.Convert(previous.GetForeground()), profile.Convert(token.GetForeground()), "adjacent byte counts need distinct ink")
				require.NotEqual(t, profile.Convert(s.tokenSelected.GetBackground()), profile.Convert(token.GetBackground()), "reserve the selection fill")
			}
		}
	}
}

func TestTokenizerStudioSummarizesSelectedSpaceByView(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height, m.color = 120, 30, true
	m.insert("We caught the last train today. ")
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{
		{2167, 2}, {18341, 9}, {290, 13}, {2174, 18},
		{8513, 24}, {4044, 30}, {13, 31}, {220, 32},
	}})
	for tab := range 3 {
		m.tab = tab
		view := ansi.Strip(m.View().Content)
		require.Contains(t, view, "Token 8 of 8  1 space")
		require.Equal(t, tab == 1, strings.Contains(view, "This encoding uses ID 220"))
		require.Equal(t, tab == 2, strings.Contains(view, "Starts after 31 bytes"))
		require.Equal(t, tab == 2, strings.Contains(view, "Uses 1 byte"))
	}

	m.modal = tokenizerModalDetails
	details := ansi.Strip(m.View().Content)
	for _, field := range []string{`Text      " "  1 space`, "Token ID  220", "Starts    After 31 bytes", "Size      1 byte", "Hex       20"} {
		require.Contains(t, details, field)
	}
}

func TestTokenizerSpaceLabelDescribesOnlyBlankSeparators(t *testing.T) {
	for fragment, label := range map[string]string{
		" ": "1 space", "\u00a0": "1 Unicode space", " \u3000": "2 Unicode spaces",
		" leading": "", "tab\t": "", "\r\n": "", "": "",
	} {
		require.Equal(t, label, tokenizerSpaceLabel(fragment))
	}
}

func TestTokenByteCountsWindowKeepsSelectedValueAndNeighbors(t *testing.T) {
	m := testTokenizerEditor()
	const count = 99
	var draft strings.Builder
	var tokens []tokenizerPreviewToken
	for size := 1; size <= count; size++ {
		draft.WriteString(strings.Repeat("x", size))
		tokens = append(tokens, tokenizerPreviewToken{ID: uint32(1000000000 + size), EndByte: uint32(draft.Len())})
	}
	m.insert(draft.String())
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
	digits := regexp.MustCompile(`[0-9]+`)
	for _, width := range []int{49, 61, 73} {
		for tab := range 3 {
			m.tab = tab
			for _, selected := range []int{0, 1, count / 2, count - 2, count - 1} {
				m.selected = selected
				for _, color := range []bool{false, true} {
					m.color = color
					start := m.resultStart(width)
					row := m.tokenByteCounts(m.styles(), width, start, m.resultEnd(start, width))
					plain := strings.TrimSpace(ansi.Strip(row))
					values, found := strings.CutPrefix(plain, "Bytes per token")
					require.True(t, found)
					require.NotContains(t, values, "\n")
					require.Contains(t, row, m.styles().tokenSelected.Render(strconv.Itoa(selected+1)))
					visible := digits.FindAllString(values, -1)
					require.Greater(t, len(visible), 1)
					first, err := strconv.Atoi(visible[0])
					require.NoError(t, err)
					for i, value := range visible {
						require.Equal(t, strconv.Itoa(first+i), value, "byte counts stay contiguous")
					}
					values = strings.TrimSpace(values)
					require.Equal(t, first > 1, strings.HasPrefix(values, "…"))
					require.Equal(t, first+len(visible)-1 < count, strings.HasSuffix(values, "…"))
					require.LessOrEqual(t, ansi.StringWidth(row), width)
					if tab == 1 {
						ids := strings.Fields(ansi.Strip(strings.Join(m.resultLines(m.styles(), width), "\n")))
						for i := range visible {
							require.Contains(t, ids, strconv.Itoa(1000000000+first+i), "each byte count belongs to a displayed ID")
						}
					}
				}
			}
		}
	}
}

func TestTokenizerStudioOmitsByteCountsUntilTokensAreReady(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height = 110, 28
	check := func(count string) {
		t.Helper()
		rows := m.studioResults(m.styles(), m.resultWidth())
		require.Len(t, rows, 1)
		require.Equal(t, count, strings.TrimSpace(ansi.Strip(rows[0])))
	}
	check("0 tokens")
	m.insert("é")
	check("Tokens")
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{{ID: 1, EndByte: 2}}})
	rows := m.studioResults(m.styles(), m.resultWidth())
	require.Len(t, rows, 3)
	require.NotContains(t, ansi.Strip(strings.Join(rows, "\n")), "1 token")
	require.Contains(t, ansi.Strip(rows[0]), "é")
	require.Contains(t, ansi.Strip(rows[1]), "Bytes per token")
	require.Contains(t, rows[1], m.styles().tokenSelected.Render("2"))
	require.Contains(t, ansi.Strip(rows[2]), "Token 1 of 1  “é”")

	m.insert("x")
	m.Update(tokenizerEditorResultMsg{Revision: m.revision})
	check("Count unavailable")
	require.Contains(t, m.View().Content, "press r")
}

func TestTokenizerStudioKeepsLongByteMetadataWithinPane(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height, m.tab = 110, 28, 2
	m.insert(strings.Repeat("a", 1<<20))
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{
		{ID: 1, EndByte: 100000}, {ID: 4294967295, EndByte: 1 << 20},
	}})
	m.selected = 1
	const width = 49
	rows := m.studioResults(m.styles(), width)
	for _, row := range rows {
		require.LessOrEqual(t, tokenizerSourceWidth(row), width)
	}
	text := ansi.Strip(strings.Join(rows, "\n"))
	require.Contains(t, text, "of 1048576 bytes")
	require.Contains(t, text, "Starts after 100000 bytes")
	require.Contains(t, text, "Uses 948576 bytes")
	require.Equal(t, []string{"Bytes", "per", "token", "100000", "948576"}, strings.Fields(ansi.Strip(rows[1])))
	require.Contains(t, rows[1], m.styles().tokenSelected.Render("948576"))

	m.tokens = []tokenizerPreviewToken{{EndByte: 1 << 20}}
	m.selected = 0
	require.Contains(t, m.tokenByteCounts(m.styles(), width, 0, 1), m.styles().tokenSelected.Render("1048576"))
}

func TestTokenizerStudioShowsLastByteCountAtMinimumSize(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height = 96, 16
	m.insert(strings.Repeat("a", 1000) + "é")
	tokens := make([]tokenizerPreviewToken, len(m.text))
	for i := range tokens {
		tokens[i] = tokenizerPreviewToken{ID: 4294967295, EndByte: uint32(i + 1)}
	}
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
	for tab := range 3 {
		m.tab = tab
		frame := m.View().Content
		view := ansi.Strip(frame)
		require.Contains(t, view, "Bytes per token")
		require.Contains(t, frame, m.styles().tokenSelected.Render("1"))
		require.Contains(t, view, "Token 1002 of 1002 (partial UTF-8)")
		require.Contains(t, view, "0xa9")
		require.Equal(t, tab == 1, strings.Contains(view, "This encoding uses ID 4294967295"))
		require.Equal(t, tab == 2, strings.Contains(view, "Starts after 1001 bytes"))
		require.Equal(t, tab == 2, strings.Contains(view, "Uses 1 byte"))
		require.Contains(t, view, "Ctrl+C exit")
		for _, row := range strings.Split(view, "\n") {
			require.Less(t, ansi.StringWidth(row), m.width)
		}
	}
}
