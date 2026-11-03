package tokenize

import (
	"fmt"
	"image/color"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// Mark matching cell styles in the rendered source pane, excluding the token strip and settings.
func tokenizerMarkedSource(t *testing.T, m *tokenizerEditor, style uv.Style) string {
	t.Helper()
	var source strings.Builder
	inSource := false
	for _, row := range strings.Split(m.View().Content, "\n") {
		left, _, _ := strings.Cut(row, "╎")
		label := strings.TrimLeft(ansi.Strip(left), " │›")
		if strings.HasPrefix(label, "View ") {
			break
		}
		inSource = inSource || strings.HasPrefix(label, "Text ")
		if !inSource {
			continue
		}
		marking := false
		for _, cell := range uv.NewStyledString(left).Lines(ansi.GraphemeWidth)[0] {
			if cell.Content == "" {
				continue
			}
			selected := cell.Style.Equal(&style)
			if selected && !marking {
				source.WriteByte('[')
			} else if !selected && marking {
				source.WriteByte(']')
			}
			source.WriteString(cell.Content)
			marking = selected
		}
		if marking {
			source.WriteByte(']')
		}
		source.WriteByte('\n')
	}
	require.True(t, inSource, "the Text pane must be visible")
	return source.String()
}

func TestTokenizerSourceSelectionFollowsTheInspectedOccurrence(t *testing.T) {
	dark := uv.Style{Fg: color.RGBA{0xf2, 0xf4, 0xfa, 0xff}, Bg: color.RGBA{0x34, 0x3d, 0x58, 0xff}}
	light := uv.Style{Fg: color.RGBA{0x20, 0x28, 0x3b, 0xff}, Bg: color.RGBA{0xe9, 0xef, 0xfe, 0xff}}
	for _, theme := range []struct {
		name  string
		style uv.Style
	}{
		{"dark", dark},
		{"light", light},
		{"monochrome", uv.Style{Attrs: uv.AttrReverse}},
	} {
		t.Run(theme.name, func(t *testing.T) {
			m := testTokenizerEditor()
			m.width, m.height, m.color, m.dark = 120, 30, theme.name != "monochrome", theme.name == "dark"
			m.Update(tea.PasteMsg{Content: "echo echo echo"})
			m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{
				{ID: 1, EndByte: 4}, {ID: 2, EndByte: 9}, {ID: 2, EndByte: 14},
			}})
			tokenizerEditorKey(m, tea.KeyHome)
			tokenizerEditorKey(m, tea.KeyRight)
			require.Equal(t, "c", tokenizerCursorCell(t, m.View()))
			tokenizerEditorKey(m, tea.KeyTab)
			tokenizerEditorKey(m, tea.KeyHome)
			tokenizerEditorKey(m, tea.KeyRight)
			require.Contains(t, tokenizerMarkedSource(t, m, theme.style), "echo[ echo] echo")
			tokenizerEditorKey(m, tea.KeyRight)
			require.Contains(t, tokenizerMarkedSource(t, m, theme.style), "echo echo[ echo]")
			require.Nil(t, m.View().Cursor)
			if theme.name == "dark" {
				m.Update(tea.BackgroundColorMsg{Color: color.White})
				require.Contains(t, tokenizerMarkedSource(t, m, light), "echo echo[ echo]")
				m.Update(tea.BackgroundColorMsg{Color: color.Black})
				require.Contains(t, tokenizerMarkedSource(t, m, dark), "echo echo[ echo]")
			}

			tokenizerEditorModifiedKey(m, tea.KeyTab, tea.ModShift)
			require.Contains(t, tokenizerMarkedSource(t, m, theme.style), "echo echo echo")
			tokenizerEditorKey(m, tea.KeyTab)
			require.Contains(t, tokenizerMarkedSource(t, m, theme.style), "echo echo[ echo]")
			tokenizerEditorKey(m, tea.KeyTab)
			require.Contains(t, tokenizerMarkedSource(t, m, theme.style), "echo echo echo")
			require.Equal(t, len("echo echo"), m.cursor)
			require.Equal(t, " ", tokenizerCursorCell(t, m.View()))
			m.Update(tea.KeyPressMsg{Code: '!', Text: "!"})
			require.Equal(t, "echo echo! echo", m.text)
		})
	}
}

func TestTokenizerSourceSelectionMarksPartialGraphemesAndLineBreaks(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {40, 12}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := testTokenizerEditor()
			m.width, m.height = size[0], size[1]
			const cluster = "👩‍💻"
			const source = "a" + cluster + "\nb"
			m.Update(tea.PasteMsg{Content: source})
			m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{
				{EndByte: 1}, {EndByte: 3}, {EndByte: uint32(1 + len(cluster))},
				{EndByte: uint32(2 + len(cluster))}, {EndByte: uint32(len(source))},
			}})
			tokenizerEditorKey(m, tea.KeyTab)
			tokenizerEditorKey(m, tea.KeyHome)
			for _, want := range []string{"a[" + cluster + "]", "a[" + cluster + "]", cluster + "[↵]"} {
				tokenizerEditorKey(m, tea.KeyRight)
				frame := m.View().Content
				require.True(t, utf8.ValidString(frame))
				require.Contains(t, frame, cluster, "style changes cannot divide a joined emoji")
				rendered := tokenizerMarkedSource(t, m, uv.Style{Attrs: uv.AttrReverse})
				require.Contains(t, rendered, want)
				require.Equal(t, 1, strings.Count(rendered, "["))
			}
			tokenizerEditorKey(m, tea.KeyLeft) // This token starts inside the joined emoji.
			tokenizerEditorKey(m, tea.KeyTab)
			require.Equal(t, len("a"), m.cursor)
			m.Update(tea.KeyPressMsg{Code: '!', Text: "!"})
			require.Equal(t, "a!"+cluster+"\nb", m.text)
		})
	}
}

func TestTokenizerSourceSelectionMarksAnOversizedGrapheme(t *testing.T) {
	for _, size := range [][2]int{{96, 16}, {40, 12}} {
		m := testTokenizerEditor()
		m.width, m.height = size[0], size[1]
		source := strings.Repeat("👩\u200d", 17) + "👩"
		m.Update(tea.PasteMsg{Content: source})
		m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{
			{EndByte: 2}, {EndByte: uint32(len(source))},
		}})
		tokenizerEditorKey(m, tea.KeyTab)
		tokenizerEditorKey(m, tea.KeyHome)
		require.Contains(t, tokenizerMarkedSource(t, m, uv.Style{Attrs: uv.AttrReverse}), "[…]",
			"the visible placeholder represents the selected grapheme at %dx%d", size[0], size[1])
	}
}

func TestTokenizerSourceSelectionTracksResultPages(t *testing.T) {
	for _, size := range [][2]int{{96, 16}, {40, 12}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := testTokenizerEditor()
			m.width, m.height = size[0], size[1]
			var source strings.Builder
			var tokens []tokenizerPreviewToken
			for i := range 80 {
				fmt.Fprintf(&source, "x%02d", i)
				tokens = append(tokens, tokenizerPreviewToken{ID: uint32(i), EndByte: uint32(source.Len())})
			}
			m.Update(tea.PasteMsg{Content: source.String()})
			m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
			tokenizerEditorKey(m, tea.KeyHome)
			tokenizerEditorKey(m, tea.KeyTab)
			tokenizerEditorKey(m, tea.KeyHome)
			for _, key := range []rune{tea.KeyPgDown, tea.KeyEnd, tea.KeyPgUp, tea.KeyHome} {
				previous := m.selected
				tokenizerEditorKey(m, key)
				if key == tea.KeyHome {
					require.Zero(t, m.selected)
				} else {
					require.NotEqual(t, previous, m.selected)
				}
				require.Contains(t, tokenizerMarkedSource(t, m, uv.Style{Attrs: uv.AttrReverse}), fmt.Sprintf("[x%02d]", m.selected))
			}
			tokenizerEditorKey(m, tea.KeyEnd)
			tokenizerEditorKey(m, tea.KeyTab)
			require.Equal(t, strings.LastIndex(m.text, "x79"), m.cursor)
			require.Equal(t, "x", tokenizerCursorCell(t, m.View()))
		})
	}
}

func TestTokenizerSourceSelectionAtAFullLineAndAcrossWrappedRows(t *testing.T) {
	for _, size := range []struct{ width, height, sourceColumns int }{{96, 16, 30}, {40, 12, 25}} {
		for _, newline := range []string{"\n", "\r\n"} {
			t.Run(fmt.Sprintf("%dx%d/%q", size.width, size.height, newline), func(t *testing.T) {
				m := testTokenizerEditor()
				m.width, m.height = size.width, size.height
				prefix := strings.Repeat("p", size.sourceColumns)
				long := "START" + strings.Repeat("Q", 120)
				source := prefix + newline + long + "!"
				m.Update(tea.PasteMsg{Content: source})
				m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{
					{EndByte: uint32(len(prefix))}, {EndByte: uint32(len(prefix + newline))},
					{EndByte: uint32(len(source) - 1)}, {EndByte: uint32(len(source))},
				}})
				tokenizerEditorKey(m, tea.KeyTab)
				tokenizerEditorKey(m, tea.KeyHome)
				tokenizerEditorKey(m, tea.KeyRight)
				style := uv.Style{Attrs: uv.AttrReverse}
				marker := strings.ReplaceAll(strings.TrimSuffix(newline, "\n"), "\r", `\r`) + "↵"
				require.Contains(t, tokenizerMarkedSource(t, m, style), "["+marker+"]")
				tokenizerEditorKey(m, tea.KeyRight)
				marked := tokenizerMarkedSource(t, m, style)
				require.Contains(t, marked, "[STARTQ")
				require.Equal(t, m.sourceRows(), strings.Count(marked, "["), "the long token fills each visible source row")
				for _, row := range strings.Split(strings.TrimSpace(marked), "\n") {
					_, after, found := strings.Cut(row, "[")
					require.True(t, found)
					span, _, found := strings.Cut(after, "]")
					require.True(t, found)
					require.Positive(t, strings.Count(span, "Q"))
					require.Equal(t, strings.Count(row, "Q"), strings.Count(span, "Q"))
				}
				require.Equal(t, source, m.text)
			})
		}
	}
}
