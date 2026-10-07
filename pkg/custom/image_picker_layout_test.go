package custom

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
)

func TestImagePickerLayoutCommonSettingsVisible(t *testing.T) {
	m := pickerForTest(t)
	m.width, m.height = 80, 24
	m.settings.count, m.settings.background = "3", "transparent"
	var ids []string
	for _, row := range m.rows() {
		ids = append(ids, row.id)
	}
	require.Equal(t, []string{"model", "size", "quality", "count", "background", "folder", "more"}, ids)
	for _, focus := range []string{"prompt", "options"} {
		m.focus = focus
		view := ansi.Strip(m.View().Content)
		require.Contains(t, view, "╭─ Prompt")
		for _, row := range m.rows() {
			require.Contains(t, view, row.label, "focus=%s row=%s", focus, row.id)
		}
		require.Contains(t, view, "Transparent")
		require.Contains(t, view, "Ctrl+C exit")
	}
	pickerKey(m, tea.KeyEscape)
	pickerKey(m, tea.KeyTab)
	pickerKey(m, tea.KeyEnd)
	pickerKey(m, tea.KeyEnter)
	require.Equal(t, "more", m.page)
	require.Equal(t, []string{"format", "back"}, []string{m.rows()[0].id, m.rows()[1].id})
	require.Len(t, m.rows(), 2)
}

func TestImagePickerLayoutPromotedSettingsKeepConstraints(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {49, 24}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := pickerForTest(t)
			m.width, m.height = size[0], size[1]
			m.settings.format = "jpeg"
			pickerKey(m, tea.KeyTab)
			pickerLayoutOpenRow(t, m, "count")
			pickerLayoutChooseValue(t, m, "10")
			require.Equal(t, "settings", m.page)
			require.Equal(t, "10", m.settings.count)
			pickerLayoutOpenRow(t, m, "background")
			pickerLayoutChooseValue(t, m, "transparent")
			require.Equal(t, "png", m.settings.format)
			require.Contains(t, m.note, "transparency")
			pickerLayoutOpenRow(t, m, "more")
			pickerLayoutOpenRow(t, m, "format")
			pickerLayoutChooseValue(t, m, "jpeg")
			require.Equal(t, "more", m.page)
			require.Equal(t, "opaque", m.settings.background)
			require.Contains(t, m.note, "JPEG")
			pickerLayoutOpenRow(t, m, "back")
			require.Equal(t, "settings", m.page)
			require.Equal(t, "more", m.rows()[m.selected].id)
			require.Empty(t, m.result.Args, "choosing settings must not generate")
			pickerKey(m, tea.KeyEscape)
			require.NotNil(t, pickerKey(m, tea.KeyEnter))
			require.Equal(t, "10", pickerArg(t, m.result.Args, "--count"))
			require.Equal(t, "opaque", pickerArg(t, m.result.Args, "--background"))
			require.Equal(t, "jpeg", pickerArg(t, m.result.Args, "--output-format"))
		})
	}
}

func TestImagePickerLayoutModelChangeKeepsCompatibleQuality(t *testing.T) {
	m := pickerForTest(t)
	pickerKey(m, tea.KeyTab)
	pickerLayoutOpenRow(t, m, "quality")
	pickerLayoutChooseValue(t, m, "max")
	pickerLayoutOpenRow(t, m, "model")
	pickerLayoutChooseValue(t, m, openai.ImageModelGPTImage1Mini)
	require.Equal(t, "auto", m.settings.quality)
	pickerLayoutOpenRow(t, m, "quality")
	var values []string
	for _, row := range m.rows() {
		values = append(values, row.value)
	}
	require.Equal(t, []string{"auto", "low", "medium", "high"}, values)
	require.Empty(t, m.result.Args)
}

func TestImagePickerLayoutChoicesStayAccessibleAfterResize(t *testing.T) {
	m := pickerForTest(t)
	pickerKey(m, tea.KeyTab)
	pickerLayoutOpenRow(t, m, "count")
	pickerKey(m, tea.KeyEnd)
	for _, size := range [][2]int{{80, 24}, {40, 12}, {49, 24}, {50, 21}, {80, 24}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		require.Equal(t, "10", m.rows()[m.selected].value)
		view := m.View()
		require.False(t, view.AltScreen)
		require.Contains(t, ansi.Strip(view.Content), "› 10")
		require.Contains(t, ansi.Strip(view.Content), "Ctrl+C exit")
		lines := strings.Split(view.Content, "\n")
		require.LessOrEqual(t, len(lines), size[1]-3)
		for _, line := range lines {
			require.LessOrEqual(t, ansi.StringWidth(line), size[0])
		}
	}
	require.Equal(t, "1", m.settings.count, "resizing must not accept the highlighted choice")
	for _, focus := range []string{"command", "prompt", "options"} {
		pickerKey(m, tea.KeyTab)
		require.Equal(t, focus, m.focus)
		require.Equal(t, "choose", m.page)
	}
	for _, focus := range []string{"prompt", "command", "options"} {
		m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
		require.Equal(t, focus, m.focus)
		require.Equal(t, "choose", m.page)
	}
	pickerKey(m, tea.KeyEnter)
	require.Equal(t, "10", m.settings.count)
	require.Equal(t, "settings", m.page)
	require.Empty(t, m.result.Args)
}

func TestImagePickerLayoutUnicodePromptSurvivesSettingsAndResize(t *testing.T) {
	m := pickerForTest(t)
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m.Update(tea.PasteMsg{Content: "雪 e\u0301 👨‍👩‍👧‍👦"})
	for _, size := range [][2]int{{80, 24}, {40, 12}, {49, 24}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		pickerKey(m, tea.KeyTab)
		pickerLayoutOpenRow(t, m, "background")
		pickerLayoutChooseValue(t, m, "transparent")
		m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
		require.Equal(t, "prompt", m.focus)
		pickerKey(m, tea.KeyHome)
		pickerKey(m, tea.KeyDelete)
		m.Update(tea.PasteMsg{Content: "雪"})
		pickerKey(m, tea.KeyEnd)
		require.Equal(t, "雪 e\u0301 👨‍👩‍👧‍👦", m.settings.prompt)
		require.Equal(t, len(m.draft), m.cursor)
		require.True(t, utf8.ValidString(m.View().Content))
		require.Contains(t, ansi.Strip(m.View().Content), "▏")
	}
	require.Empty(t, m.result.Args)
}

func TestImagePickerLayoutNoColorKeepsSelectionVisible(t *testing.T) {
	// runImagePicker disables model color when NO_COLOR is set.
	// Test the view's color-disabled branch across compact and framed layouts.
	sgr := regexp.MustCompile(`\x1b\[[0-9;:]*m`)
	for _, size := range [][2]int{{40, 12}, {49, 24}, {80, 24}} {
		m := pickerForTest(t)
		m.width, m.height, m.color = size[0], size[1], false
		pickerKey(m, tea.KeyTab)
		pickerLayoutOpenRow(t, m, "background")
		pickerKey(m, tea.KeyEnd)
		view := m.View().Content
		for _, sequence := range sgr.FindAllString(view, -1) {
			require.Regexp(t, `^\x1b\[(?:0|1|22)?m$`, sequence, "NO_COLOR permits emphasis without SGR colors")
		}
		require.Contains(t, ansi.Strip(view), "› Transparent")
		require.Contains(t, ansi.Strip(view), "Auto  ✓")
		require.Contains(t, ansi.Strip(view), "Ctrl+C exit")
	}
}

func pickerLayoutOpenRow(t *testing.T, m *imagePicker, id string) {
	t.Helper()
	require.Equal(t, "options", m.focus)
	pickerKey(m, tea.KeyHome)
	for range len(m.rows()) {
		row := m.rows()[m.selected]
		if row.id == id {
			require.Contains(t, ansi.Strip(m.View().Content), "› "+row.label)
			require.Nil(t, pickerKey(m, tea.KeyEnter))
			require.Empty(t, m.result.Args)
			return
		}
		pickerKey(m, tea.KeyDown)
	}
	t.Fatalf("row %q is unavailable on page %q", id, m.page)
}

func pickerLayoutChooseValue(t *testing.T, m *imagePicker, value string) {
	t.Helper()
	require.Equal(t, "choose", m.page)
	pickerKey(m, tea.KeyHome)
	for range len(m.rows()) {
		row := m.rows()[m.selected]
		if row.value == value {
			require.Contains(t, ansi.Strip(m.View().Content), "› "+row.label)
			require.Nil(t, pickerKey(m, tea.KeyEnter))
			require.Empty(t, m.result.Args)
			return
		}
		pickerKey(m, tea.KeyDown)
	}
	t.Fatalf("value %q is unavailable for field %q", value, m.field)
}
