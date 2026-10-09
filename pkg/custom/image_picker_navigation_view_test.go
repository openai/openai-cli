package custom

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestImagePickerChoiceColumnsStayAligned(t *testing.T) {
	for _, width := range []int{40, 80} {
		for _, color := range []bool{false, true} {
			t.Run(fmt.Sprintf("width=%d/color=%t", width, color), func(t *testing.T) {
				m := pickerForTest(t)
				m.width, m.color, m.focus = width, color, "options"
				detailColumn, checkColumn := -1, -1
				for _, format := range []string{"png", "jpeg", "webp"} {
					m.settings.format = format
					m.choose("format")
					view := m.View().Content
					require.Equal(t, view, m.View().Content, "rendering must be idempotent")
					_, jpeg := pickerNavigationLine(t, view, "JPEG")
					_, checked := pickerNavigationLine(t, view, "✓")
					detail := ansi.StringWidth(strings.Split(jpeg, "No transparency")[0])
					check := ansi.StringWidth(strings.Split(checked, "✓")[0])
					if detailColumn < 0 {
						detailColumn, checkColumn = detail, check
					}
					require.Contains(t, jpeg, "No transparency")
					require.Equal(t, detailColumn, detail, "committing JPEG must not shift its description")
					require.Equal(t, checkColumn, check, "all choices share one checkmark column")
					for _, label := range []string{"PNG", "JPEG", "WEBP"} {
						_, line := pickerNavigationLine(t, view, label)
						require.Equal(t, 4, ansi.StringWidth(strings.Split(line, label)[0]))
					}
					pickerLayoutAssertBounds(t, m)
				}
			})
		}
	}
}

func TestImagePickerResumedFocusKeepsLayoutStable(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {80, 24}} {
		for _, note := range []string{"", "All images saved. Edit your prompt or settings to create more."} {
			t.Run(fmt.Sprintf("%dx%d/note=%t", size[0], size[1], note != ""), func(t *testing.T) {
				m := pickerForTest(t)
				m.width, m.height, m.hidePromptHint = size[0], size[1], true
				m.insertPrompt(strings.Repeat(" orange robot", 8))
				m.note = note
				before := ansi.Strip(m.View().Content)
				beforeCommand, _ := pickerNavigationLine(t, before, "openai images generate")
				require.NotContains(t, before, "Ctrl+C exit")
				pickerKey(m, tea.KeyDown)
				after := ansi.Strip(m.View().Content)
				afterCommand, _ := pickerNavigationLine(t, after, "openai images generate")
				require.Equal(t, strings.Count(before, "\n"), strings.Count(after, "\n"))
				require.Equal(t, beforeCommand, afterCommand, "focus must not move the command preview")
				for _, row := range m.rows() {
					require.Equal(t, strings.Contains(before, row.label), strings.Contains(after, row.label))
				}
				require.Contains(t, after, "Ctrl+C exit")
				pickerKey(m, tea.KeyUp)
				require.Equal(t, before, ansi.Strip(m.View().Content))
				require.Empty(t, m.result.Args)
				pickerLayoutAssertBounds(t, m)
			})
		}
	}
}

func TestImagePickerOptionNavigationScrollsOnlyAtEdges(t *testing.T) {
	m := pickerForTest(t)
	m.width, m.height, m.focus, m.settings.count = 40, 12, "options", "10"
	m.choose("count")
	require.Equal(t, []int{8, 9, 10}, pickerVisibleCounts(m.View().Content))
	for _, step := range []struct {
		key      rune
		selected int
		visible  []int
	}{
		{tea.KeyUp, 8, []int{8, 9, 10}},
		{tea.KeyUp, 7, []int{8, 9, 10}},
		{tea.KeyUp, 6, []int{7, 8, 9}},
		{tea.KeyDown, 7, []int{7, 8, 9}},
		{tea.KeyDown, 8, []int{7, 8, 9}},
		{tea.KeyDown, 9, []int{8, 9, 10}},
	} {
		pickerKey(m, step.key)
		view := m.View().Content
		require.Equal(t, step.visible, pickerVisibleCounts(view))
		require.Equal(t, step.selected, m.selected)
		require.Equal(t, view, m.View().Content)
		require.Equal(t, "10", m.settings.count, "navigation must not commit a choice")
		require.Empty(t, m.result.Args)
	}
	for _, size := range [][2]int{{80, 24}, {39, 11}, {40, 12}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := m.View().Content
		if size[0] < 40 {
			require.Contains(t, view, "Esc prompt · Ctrl+C quit")
		} else {
			require.Contains(t, pickerVisibleCounts(view), 10)
		}
		require.Equal(t, 9, m.selected, "resizing must not change selection")
		require.Equal(t, view, m.View().Content)
		pickerLayoutAssertBounds(t, m)
	}
	m.choose("format")
	require.Contains(t, ansi.Strip(m.View().Content), "PNG")
	require.Zero(t, m.optionOffset, "a new field starts its own viewport")
	pickerKey(m, tea.KeyEscape)
	require.Contains(t, ansi.Strip(m.View().Content), "Model")
	require.Zero(t, m.optionOffset, "returning to settings resets the viewport")
}

func pickerNavigationLine(t *testing.T, view, text string) (int, string) {
	t.Helper()
	for i, line := range strings.Split(ansi.Strip(view), "\n") {
		if strings.Contains(line, text) {
			return i, line
		}
	}
	t.Fatalf("missing %q in view:\n%s", text, ansi.Strip(view))
	return 0, ""
}

func pickerVisibleCounts(view string) []int {
	var counts []int
	for _, line := range strings.Split(ansi.Strip(view), "\n") {
		fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "› "))
		if len(fields) > 0 {
			if count, err := strconv.Atoi(fields[0]); err == nil {
				counts = append(counts, count)
			}
		}
	}
	return counts
}
