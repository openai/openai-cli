package commandsuggest

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestCommandPickerSearchSelectAndCancel(t *testing.T) {
	candidates := []Candidate{{"openai models list", "List models"}, {"openai help setup", "Configure credentials"}}
	m := newCommandPicker(candidates, "Recent commands", true)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.PasteMsg{Content: "credentials"})
	require.Equal(t, []int{1}, m.matches)
	require.Empty(t, m.result)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	require.Equal(t, "openai help setup", m.result)
	require.True(t, m.done)
	for _, msg := range []tea.Msg{tea.KeyPressMsg{Code: tea.KeyEnter}, tea.PasteMsg{Content: "bad"}, tea.KeyPressMsg{Code: tea.KeyDown}} {
		m.Update(msg)
		require.Equal(t, "openai help setup", m.result)
	}
	require.Equal(t, "\n\n", m.View().Content, "retain cursor rows until renderer shutdown completes")
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEscape}, {Code: 'c', Mod: tea.ModCtrl}} {
		m := newCommandPicker(candidates, "Recent commands", true)
		_, cmd := m.Update(key)
		require.NotNil(t, cmd)
		require.Empty(t, m.result)
		require.True(t, m.done)
	}
}

func TestCommandPickerHighlightsSearchWithoutChangingSelectedCommand(t *testing.T) {
	for _, tc := range []struct {
		name, command, description, query, commandMatch, descriptionMatch string
	}{
		{"case", "openai models list", "List available models", "MoDeLs", "models", "models"},
		{"multiple words", "openai models list", "List available models", "models list", "list", "List"},
		{"changing UTF-8 length", "openai future --name 'İK'", "Unicode", "ik", "İK", ""},
		{"combining mark", "openai future --name 'e\u0301'", "Unicode", "e", "e\u0301", ""},
		{"emoji modifier", "openai future --name '👍🏽'", "Emoji", "👍", "👍🏽", ""},
		{"overlap", "openai future --name banana", "Overlap", "ban bana", "bana", ""},
		{"description only", "openai help setup", "Configure credentials", "credentials", "", "credentials"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, color := range []bool{false, true} {
				m := newCommandPicker([]Candidate{{tc.command, tc.description}, {tc.command + " --format json", tc.description}}, "Commands", true)
				m.color, m.backgroundKnown = color, true
				m.Update(tea.WindowSizeMsg{Width: 94, Height: 15})
				m.Update(tea.PasteMsg{Content: tc.query})
				view := m.View().Content
				require.Contains(t, ansi.Strip(view), tc.command)
				require.NotContains(t, view, "\x1b[4m")
				if tc.commandMatch != "" {
					require.Contains(t, view, "\x1b[1m"+tc.commandMatch+"\x1b[22m")
				}
				if tc.descriptionMatch != "" {
					require.Contains(t, view, "\x1b[1m"+tc.descriptionMatch+"\x1b[22m")
				}
				for _, line := range strings.Split(view, "\n") {
					if strings.HasPrefix(ansi.Strip(line), "  › ") {
						// The selected command remains one uninterrupted styled span.
						commandSpan := line[strings.Index(line, tc.command):]
						require.NotContains(t, strings.TrimSuffix(commandSpan, ansi.ResetStyle), ansi.ResetStyle)
					}
				}
				require.Empty(t, m.result)
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				require.Equal(t, tc.command, m.result)
			}
		})
	}
}

func TestCommandPickerClippedMatchesEndBeforeFooter(t *testing.T) {
	word := strings.Repeat("界", 80)
	for _, candidate := range []Candidate{
		{Command: "openai future --name '" + word + "'"},
		{Command: "openai future", Description: word},
	} {
		m := newCommandPicker([]Candidate{candidate}, "Commands", true)
		m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
		m.Update(tea.PasteMsg{Content: word})
		view := m.View().Content
		detailEnd := strings.Index(view, "\n  \n  ↑↓")
		require.Positive(t, detailEnd)
		require.Contains(t, view[:detailEnd], "\x1b[1m界")
		require.True(t, strings.HasSuffix(view[:detailEnd], "\x1b[22m"))
		for _, line := range strings.Split(view, "\n") {
			require.LessOrEqual(t, ansi.StringWidth(line), 39)
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		require.Equal(t, candidate.Command, m.result)
	}
}

func TestCommandPickerFramePreservesWrappedHighlights(t *testing.T) {
	word := strings.Repeat("界", 80)
	for _, color := range []bool{false, true} {
		m := newCommandPicker([]Candidate{{Command: "openai future", Description: word}}, "Commands", true)
		m.color, m.backgroundKnown = color, true
		m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
		m.Update(tea.PasteMsg{Content: word})
		rows := 0
		for _, line := range strings.Split(m.View().Content, "\n") {
			if !strings.HasPrefix(ansi.Strip(line), "  │   界") {
				continue
			}
			rows++
			on, off := strings.Index(line, "\x1b[1m"), strings.LastIndex(line, "\x1b[22m")
			require.GreaterOrEqual(t, on, 0)
			require.Less(t, on, strings.Index(line, "界"))
			require.Greater(t, off, strings.LastIndex(line, "界"))
			require.Less(t, off, strings.LastIndex(line, "│"))
		}
		require.Equal(t, 2, rows)
	}
}

func TestCommandPickerNoMatchesAndSmallWindowCannotSelect(t *testing.T) {
	m := newCommandPicker([]Candidate{{Command: "openai models list"}}, "Explore commands", false)
	m.Update(tea.WindowSizeMsg{Width: 39, Height: 11})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.False(t, m.done)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.PasteMsg{Content: "not-found"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.False(t, m.done)
	require.Contains(t, m.View().Content, "No matching commands")
}

func TestCommandPickerEditingNavigationAndRenderBounds(t *testing.T) {
	m := newCommandPicker([]Candidate{{"openai models list", "List models"}, {"openai help setup", "Setup"}, {"openai files list", "Files"}}, "Explore commands", true)
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Equal(t, 1, m.selected)
	m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	require.Zero(t, m.selected)
	m.Update(tea.PasteMsg{Content: "models list"})
	require.Equal(t, []int{0}, m.matches)
	before := string(m.query)
	m.Update(tea.PasteMsg{Content: "bad\n\r\x1b"})
	require.Equal(t, before, string(m.query))
	lines := strings.Split(m.View().Content, "\n")
	require.LessOrEqual(t, len(lines), 11)
	for _, line := range lines {
		require.LessOrEqual(t, ansi.StringWidth(line), 40)
	}
	require.Contains(t, m.View().Content, "Enter fills prompt")
	require.Contains(t, m.View().Content, "Esc cancels")
}

func TestPickerRejectsNonTerminalAndCanceledContext(t *testing.T) {
	_, err := Pick(context.Background(), nil, nil, []Candidate{{Command: "openai models list"}}, "Commands", true)
	require.Error(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Pick(ctx, nil, nil, nil, "Commands", true)
	require.ErrorIs(t, err, context.Canceled)
}

type shortPickerWriter struct{ calls int }

func (w *shortPickerWriter) Write(data []byte) (int, error) { w.calls++; return len(data) - 1, nil }

func TestCommandPickerOutputFailureCancelsAndPreservesError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := &shortPickerWriter{}
	output := &pickerOutput{writer: writer, cancel: cancel}
	_, err := io.WriteString(output, "frame")
	require.ErrorIs(t, err, io.ErrShortWrite)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.ErrorIs(t, output.failure(), io.ErrShortWrite)
	_, err = output.Write([]byte("again"))
	require.ErrorIs(t, err, io.ErrShortWrite)
	require.Equal(t, 1, writer.calls)
}

func TestCommandPickerQueryEditingAndWideCursor(t *testing.T) {
	m := newCommandPicker(nil, "Commands", true)
	m.Update(tea.PasteMsg{Content: "models list"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDelete})
	require.Equal(t, "odels list", string(m.query))
	m.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	require.Equal(t, "models list", string(m.query))
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Equal(t, "models lis", string(m.query))
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	require.Empty(t, m.query)
	m.Update(tea.PasteMsg{Content: strings.Repeat("界", 300)})
	require.Len(t, m.query, 256)
	view, cursor := m.queryView(36)
	require.LessOrEqual(t, ansi.StringWidth(view), 36)
	require.Less(t, cursor, 36)
	m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	require.Empty(t, m.query)
}

func TestCommandPickerRejectedPastePreservesSelection(t *testing.T) {
	m := newCommandPicker([]Candidate{{Command: "openai models list"}, {Command: "openai files list"}}, "Commands", true)
	m.Update(tea.PasteMsg{Content: "list"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	before := string(m.query)
	cursor := m.cursor
	matches := append([]int(nil), m.matches...)
	selected := m.selected
	m.Update(tea.PasteMsg{Content: "bad\n"})
	require.Equal(t, before, string(m.query))
	require.Equal(t, cursor, m.cursor)
	require.Equal(t, matches, m.matches)
	require.Equal(t, selected, m.selected)
}

func TestOpenPickerInputRejectsMissingOrNonTerminalOutput(t *testing.T) {
	input, err := OpenPickerInput(nil)
	require.Error(t, err)
	require.Nil(t, input)
	output, err := os.Create(filepath.Join(t.TempDir(), "output"))
	require.NoError(t, err)
	defer output.Close()
	input, err = OpenPickerInput(output)
	require.Error(t, err)
	require.Nil(t, input)
	_, err = output.WriteString("still owned by caller")
	require.NoError(t, err)
}

// Exercise the smallest supported frame and wider terminal cells. These
// assertions protect usable controls, scrolling, and cursor ownership, not styles.
func TestCommandPickerResponsiveRenderBounds(t *testing.T) {
	wideQuery := strings.Repeat("界", 120)
	wideCommand := "openai future --name '" + strings.Repeat("界🦊", 70) + "'"
	scenarios := []struct {
		name, query string
		candidates  []Candidate
		empty       bool
	}{
		{"short", "", []Candidate{{"openai models list", "List available models"}, {"openai files list", "List files"}}, false},
		{"wide", wideQuery, []Candidate{{wideCommand, wideQuery + " " + strings.Repeat("Unicode description 🦊 e\u0301 ", 50)}, {wideCommand + " --format json", wideQuery}}, false},
		{"empty", "no-such-command", []Candidate{{"openai models list", "List models"}}, true},
	}
	for _, width := range []int{40, 59, 60, 80, 120} {
		for _, height := range []int{12, 13, 15, 16, 24} {
			for _, integration := range []bool{false, true} {
				for _, scenario := range scenarios {
					t.Run(fmt.Sprintf("%dx%d/integration=%t/%s", width, height, integration, scenario.name), func(t *testing.T) {
						m := newCommandPicker(scenario.candidates, "Explore commands", integration)
						m.Update(tea.WindowSizeMsg{Width: width, Height: height})
						if scenario.query != "" {
							m.Update(tea.PasteMsg{Content: scenario.query})
						}
						// Move beyond the first result. Tiny frames must keep selection visible.
						if !scenario.empty {
							m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
						}
						view := m.View()
						plain := ansi.Strip(view.Content)
						lines := strings.Split(plain, "\n")
						require.LessOrEqual(t, len(lines), height-1)
						for _, line := range lines {
							require.LessOrEqual(t, ansi.StringWidth(line), width-1)
						}
						require.NotNil(t, view.Cursor)
						queryRow := 1
						if height > 12 {
							queryRow = 2
						}
						queryPrefix, queryStart, querySuffix := "  Search  ", 10, ""
						if width >= 60 && height >= 16 {
							queryPrefix, queryStart, querySuffix = "  │ Search  ", 12, " │"
						}
						require.Equal(t, queryRow, view.Cursor.Y)
						require.Equal(t, tea.CursorBar, view.Cursor.Shape)
						require.True(t, view.Cursor.Blink)
						require.GreaterOrEqual(t, view.Cursor.X, 10)
						require.Less(t, view.Cursor.X, width-1)
						require.True(t, strings.HasPrefix(lines[view.Cursor.Y], queryPrefix))
						if scenario.query == "" {
							require.Equal(t, queryStart, view.Cursor.X)
						} else {
							// At query end, the cursor follows its visible text, including wide cells.
							queryLine := strings.TrimRight(strings.TrimSuffix(lines[view.Cursor.Y], querySuffix), " ")
							require.Equal(t, ansi.StringWidth(queryLine), view.Cursor.X)
						}
						require.Contains(t, plain, "Esc cancels")
						if scenario.empty {
							require.Contains(t, plain, "No matching commands")
							require.NotContains(t, plain, "Enter fills prompt")
							require.NotContains(t, plain, "Enter selects")
							require.NotContains(t, plain, "› ")
						} else {
							require.Contains(t, plain, "› ")
							require.Contains(t, plain, "↑↓ choose")
							action := "Enter selects"
							if integration {
								action = "Enter fills prompt"
							}
							require.Contains(t, plain, action)
						}
					})
				}
			}
		}
	}
}

func TestCommandPickerQueryCursorTracksVisibleWideCells(t *testing.T) {
	m := newCommandPicker([]Candidate{{Command: "openai models list"}}, "Commands", true)
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m.Update(tea.PasteMsg{Content: "界e\u0301🦊"})
	view := m.View()
	require.Equal(t, 10+ansi.StringWidth("界e\u0301🦊"), view.Cursor.X)
	require.Equal(t, 1, view.Cursor.Y)
	m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	require.Equal(t, 10, m.View().Cursor.X)
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	require.Equal(t, 12, m.View().Cursor.X)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	require.Equal(t, 12+ansi.StringWidth("界e\u0301🦊"), m.View().Cursor.X)
	require.Equal(t, 2, m.View().Cursor.Y)
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	require.Equal(t, 1, m.View().Cursor.Y)
}
