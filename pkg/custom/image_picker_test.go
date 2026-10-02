package custom

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
)

func pickerForTest(t *testing.T) *imagePicker {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	m, err := newImagePicker(imagePickerOptions{Prompt: "A tiny orange robot watering a plant"})
	require.NoError(t, err)
	m.width, m.height, m.color = 90, 24, false
	return m
}

func pickerKey(m *imagePicker, code rune) tea.Cmd {
	_, cmd := m.Update(tea.KeyPressMsg{Code: code})
	return cmd
}

func pickerArg(t *testing.T, args []string, flag string) string {
	t.Helper()
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	t.Fatalf("missing flag %s", flag)
	return ""
}

func TestImagePickerPromptSubmitsExactlyOnce(t *testing.T) {
	m := pickerForTest(t)
	require.Equal(t, "prompt", m.focus)
	cmd := pickerKey(m, tea.KeyEnter)
	require.NotNil(t, cmd)
	require.IsType(t, tea.QuitMsg{}, cmd())
	require.False(t, m.result.PrintOnly)
	require.False(t, m.result.Canceled)
	require.Equal(t, []string{"images", "generate"}, m.result.Args[:2])
	result := m.result
	result.Args = append([]string(nil), m.result.Args...)
	settings := m.settings
	for range 12 {
		require.Nil(t, pickerKey(m, tea.KeyEnter), "queued Enter must not resubmit")
	}
	for _, msg := range []tea.Msg{
		tea.KeyPressMsg{Code: tea.KeyEscape},
		tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl},
		tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl},
		tea.KeyPressMsg{Code: 'x', Text: "x"},
		tea.PasteMsg{Content: "queued text\n"},
	} {
		_, cmd := m.Update(msg)
		require.Nil(t, cmd)
		require.Equal(t, result, m.result, "queued input must preserve the submitted action")
		require.Equal(t, settings, m.settings)
	}
}

func TestImagePickerCancellationCanStopPendingSubmission(t *testing.T) {
	for _, cancel := range []tea.Msg{tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, imagePickerStopMsg{code: 143}} {
		m := pickerForTest(t)
		require.NotNil(t, pickerKey(m, tea.KeyEnter))
		require.NotEmpty(t, m.result.Args)
		_, cmd := m.Update(cancel)
		require.NotNil(t, cmd)
		require.True(t, m.result.Canceled)
		require.Empty(t, m.result.Args)
		result, settings := m.result, m.settings
		require.Nil(t, pickerKey(m, tea.KeyEnter))
		m.Update(tea.PasteMsg{Content: "after cancellation"})
		require.Equal(t, result, m.result)
		require.Equal(t, settings, m.settings)
	}
}

func TestImagePickerSettingsConstraints(t *testing.T) {
	m := pickerForTest(t)
	require.Len(t, m.choices("quality"), 6)
	m.settings.quality = "max"
	m.field = "model"
	m.apply(openai.ImageModelGPTImage1Mini)
	require.Equal(t, "auto", m.settings.quality)
	require.Len(t, m.choices("quality"), 4)
	require.Contains(t, m.note, "Quality changed")
	m.settings.format = "jpeg"
	m.field = "background"
	m.apply("transparent")
	require.Equal(t, "png", m.settings.format)
	require.Contains(t, m.note, "transparency")
	m.field = "format"
	m.apply("jpeg")
	require.Equal(t, "opaque", m.settings.background)
	require.Contains(t, m.note, "JPEG")
	m.field = "count"
	require.Len(t, m.choices("count"), 10)
	m.apply("10")
	require.Equal(t, "10", m.settings.count)
}

func TestImagePickerLivePromptAndCommandUpdate(t *testing.T) {
	m := pickerForTest(t)
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m.Update(tea.PasteMsg{Content: "A 'purple' robot"})
	require.Equal(t, "A 'purple' robot", m.settings.prompt)
	require.Equal(t, "prompt", m.focus)
	preview, _, _ := m.commandPreview(1000, 1)
	require.Contains(t, preview[0], "--prompt 'A '\\''purple'\\'' robot'")
	m.field = "quality"
	m.apply("max")
	preview, _, _ = m.commandPreview(1000, 1)
	require.Contains(t, preview[0], "--quality max")
	m.field = "model"
	m.apply(openai.ImageModelGPTImage1Mini)
	preview, _, _ = m.commandPreview(1000, 1)
	require.Contains(t, preview[0], "--quality auto")
	require.Contains(t, preview[0], "--model gpt-image-1-mini")
}

func TestImagePickerSubmissionFromEveryFocus(t *testing.T) {
	for _, focus := range []string{"prompt", "command"} {
		m := pickerForTest(t)
		m.focus = focus
		cmd := pickerKey(m, tea.KeyEnter)
		require.NotNil(t, cmd)
		require.IsType(t, tea.QuitMsg{}, cmd())
		require.Equal(t, m.settings.args(), m.result.Args)
		require.False(t, m.result.PrintOnly)
	}
}

func TestImagePickerFocusCycleAndPrint(t *testing.T) {
	m := pickerForTest(t)
	for _, focus := range []string{"options", "command", "prompt"} {
		pickerKey(m, tea.KeyTab)
		require.Equal(t, focus, m.focus)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	require.Equal(t, "command", m.focus)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	require.True(t, m.result.PrintOnly)
	require.Equal(t, m.settings.prompt, pickerArg(t, m.result.Args, "--prompt"))
}

func TestImagePickerArrowsReturnToPromptWithoutClosingList(t *testing.T) {
	for _, page := range []string{"settings", "choose", "more"} {
		m := pickerForTest(t)
		m.page, m.field, m.focus = page, "model", "options"
		m.settings.model = openai.ImageModelGPTImage2_5Flare
		m.selected = 1 // Highlight a different row before returning to Prompt.
		before := m.settings
		pickerKey(m, tea.KeyUp)
		pickerKey(m, tea.KeyUp)
		require.Equal(t, "prompt", m.focus)
		require.Equal(t, page, m.page, "arrow navigation keeps the visible list")
		require.Equal(t, before, m.settings, "highlighting must not commit a value")
		pickerKey(m, tea.KeyUp) // No wrapping back to the bottom.
		m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
		require.Equal(t, before.prompt+"q", m.settings.prompt)
		require.False(t, m.result.Canceled)
		pickerKey(m, tea.KeyDown)
		require.Equal(t, "options", m.focus)
		require.Equal(t, page, m.page)
		require.Zero(t, m.selected)
		require.Empty(t, m.result.Args)
	}
}

func TestImagePickerArrowsFollowScreenOrder(t *testing.T) {
	m := pickerForTest(t)
	m.insertPrompt(strings.Repeat(" long text", 50))
	pickerKey(m, tea.KeyDown)
	pickerKey(m, tea.KeyEnd)
	m.commandOffset = 10 // Re-entering from above must start at the first line.
	pickerKey(m, tea.KeyDown)
	require.Equal(t, "command", m.focus)
	require.Zero(t, m.commandOffset)
	require.Contains(t, ansi.Strip(m.View().Content), "› openai images generate")
	pickerKey(m, tea.KeyDown)
	require.Equal(t, 1, m.commandOffset)
	pickerKey(m, tea.KeyUp)
	require.Equal(t, "command", m.focus)
	require.Zero(t, m.commandOffset)
	pickerKey(m, tea.KeyUp)
	require.Equal(t, "options", m.focus)
	require.Equal(t, len(m.rows())-1, m.selected)
	for range len(m.rows()) {
		pickerKey(m, tea.KeyUp)
	}
	require.Equal(t, "prompt", m.focus)
	require.Empty(t, m.result.Args)
}

func TestImagePickerPrintShortcutKeepsCommittedChoices(t *testing.T) {
	m := pickerForTest(t)
	pickerKey(m, tea.KeyDown)
	pickerKey(m, tea.KeyEnter) // Open model choices.
	pickerKey(m, tea.KeyDown)  // Highlight Flare without selecting it.
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	require.True(t, m.result.PrintOnly)
	require.Equal(t, openai.ImageModelGPTImage2_5Sunburst, pickerArg(t, m.result.Args, "--model"))
}

func TestImagePickerEscapeModelMenuToPrompt(t *testing.T) {
	m := pickerForTest(t)
	original := m.settings
	pickerKey(m, tea.KeyTab)
	pickerKey(m, tea.KeyEnter)
	pickerKey(m, tea.KeyDown) // Highlight Flare without choosing it.
	pickerKey(m, tea.KeyEscape)
	require.Equal(t, "settings", m.page)
	require.Equal(t, "prompt", m.focus)
	require.Equal(t, original, m.settings, "Esc must not apply the highlighted model")
	pickerKey(m, tea.KeyEscape) // Repeated Esc must not move focus away again.
	m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	require.Equal(t, original.prompt+"x", m.settings.prompt)
	require.Empty(t, m.result.Args)
	require.False(t, m.result.Canceled)
}

func TestImagePickerEscapeFromEveryFocusAndSmallWindow(t *testing.T) {
	for _, page := range []string{"settings", "choose", "more"} {
		for _, focus := range []string{"prompt", "options", "command"} {
			m := pickerForTest(t)
			m.page, m.focus, m.field = page, focus, "format"
			m.settings.quality = "high"
			original := m.settings
			m.width, m.height = 25, 7
			_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			require.Nil(t, cmd)
			require.Equal(t, "settings", m.page)
			require.Equal(t, "prompt", m.focus)
			require.Equal(t, original, m.settings)
			require.Empty(t, m.result.Args)
			require.False(t, m.result.Canceled)
			m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			m.Update(tea.KeyPressMsg{Code: 'z', Text: "z"})
			require.Equal(t, original.prompt+"z", m.settings.prompt)
		}
	}
}

func TestImagePickerFastEscapeAndTyping(t *testing.T) {
	for _, page := range []string{"settings", "choose", "more"} {
		for _, mod := range []tea.KeyMod{tea.ModAlt, tea.ModAlt | tea.ModShift} {
			m := pickerForTest(t)
			m.page, m.focus, m.field = page, "options", "model"
			original := m.settings
			// Legacy terminal input can combine Esc then a letter into Alt+letter.
			m.Update(tea.KeyPressMsg{Code: 'x', Mod: mod})
			letter := "x"
			if mod&tea.ModShift != 0 {
				letter = "X"
			}
			require.Equal(t, "prompt", m.focus)
			require.Equal(t, "settings", m.page)
			require.Equal(t, original.prompt+letter, m.settings.prompt)
			require.Equal(t, original.model, m.settings.model)
			require.Empty(t, m.result.Args)
		}
	}
}

func TestImagePickerFastEscapeWhileAlreadyAtPrompt(t *testing.T) {
	m := pickerForTest(t)
	original := m.settings.prompt
	m.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModAlt})
	for _, r := range "uiet" {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	require.Equal(t, original+"quiet", m.settings.prompt)
	require.Equal(t, "prompt", m.focus)
	require.False(t, m.result.Canceled)
	require.Empty(t, m.result.Args)
}

func TestImagePickerFastEscapeEnterLeavesMenuWithoutSubmitting(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyEnter, Mod: tea.ModAlt},
		{Code: tea.KeyEscape, Mod: tea.ModAlt},
		{Code: 'g', Mod: tea.ModAlt | tea.ModCtrl},
	} {
		m := pickerForTest(t)
		m.page, m.focus, m.field, m.selected = "choose", "options", "model", 1
		_, cmd := m.Update(key)
		require.Nil(t, cmd)
		require.Equal(t, "prompt", m.focus)
		require.Equal(t, "settings", m.page)
		require.Empty(t, m.result.Args)
		require.Equal(t, openai.ImageModelGPTImage2_5Sunburst, m.settings.model)
	}
}

func TestImagePickerCtrlGFromDropdown(t *testing.T) {
	m := pickerForTest(t)
	pickerKey(m, tea.KeyTab)
	pickerKey(m, tea.KeyEnter)
	require.Equal(t, "choose", m.page)
	pickerKey(m, tea.KeyDown)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	require.Equal(t, openai.ImageModelGPTImage2_5Sunburst, m.settings.model, "unconfirmed highlighted option must not apply")
	require.Equal(t, openai.ImageModelGPTImage2_5Sunburst, pickerArg(t, m.result.Args, "--model"))
	require.False(t, m.result.PrintOnly)
}

func TestImagePickerMoreOptions(t *testing.T) {
	m := pickerForTest(t)
	pickerKey(m, tea.KeyTab)
	pickerKey(m, tea.KeyEnd)
	pickerKey(m, tea.KeyEnter)
	require.Equal(t, "more", m.page)
	pickerKey(m, tea.KeyEnter) // background
	pickerKey(m, tea.KeyEnd)
	pickerKey(m, tea.KeyEnter) // transparent
	require.Equal(t, "transparent", m.settings.background)
	pickerKey(m, tea.KeyDown)
	pickerKey(m, tea.KeyEnter) // file type
	pickerKey(m, tea.KeyDown)
	pickerKey(m, tea.KeyEnter) // JPEG
	require.Equal(t, "jpeg", m.settings.format)
	require.Equal(t, "opaque", m.settings.background)
	pickerKey(m, tea.KeyDown)
	pickerKey(m, tea.KeyEnter) // image count
	pickerKey(m, tea.KeyEnd)
	pickerKey(m, tea.KeyEnter) // ten
	require.Equal(t, "10", m.settings.count)
	command := formatImagePickerCommandBash(m.settings.args())
	require.Contains(t, command, "--background opaque --count 10")
	require.Empty(t, m.result.Args, "selecting options must not submit")
	pickerKey(m, tea.KeyTab)
	require.NotNil(t, pickerKey(m, tea.KeyEnter))
	require.Equal(t, "10", pickerArg(t, m.result.Args, "--count"))
	require.False(t, m.result.PrintOnly)
}

func TestImagePickerTinyWindowCannotSubmit(t *testing.T) {
	for _, focus := range []string{"prompt", "options", "command"} {
		m := pickerForTest(t)
		m.focus = focus
		m.Update(tea.WindowSizeMsg{Width: 25, Height: 7})
		require.Nil(t, pickerKey(m, tea.KeyEnter))
		_, cmd := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
		require.Nil(t, cmd)
		require.Empty(t, m.result.Args)
		require.Contains(t, m.View().Content, "Resize")
	}
}

func TestImagePickerEmptyPromptPlaceholder(t *testing.T) {
	m, err := newImagePicker(imagePickerOptions{})
	require.NoError(t, err)
	m.width, m.height = 80, 24
	require.Contains(t, m.View().Content, "Describe your image")
	pickerKey(m, tea.KeyEnter)
	require.Equal(t, "settings", m.page)
	require.Equal(t, "Add a prompt first.", m.note)
	require.Empty(t, m.settings.prompt)
}

func TestImagePickerBoundedCommandPreviewShowsRange(t *testing.T) {
	m := pickerForTest(t)
	m.settings.prompt = strings.Repeat("long prompt ", 1000)
	lines, start, total := m.commandPreview(40, 2)
	require.Greater(t, total, len(lines))
	require.Zero(t, start)
	require.Len(t, lines, 2)
	require.False(t, strings.HasSuffix(lines[1], "…"))
	m.focus = "command"
	require.Contains(t, m.View().Content, "↑↓ scroll 1-4/")
	require.Contains(t, m.View().Content, "Ctrl+P print")
	require.NotContains(t, m.View().Content, "Command")
	for _, line := range lines {
		require.LessOrEqual(t, ansi.StringWidth(line), 40)
	}
}

func TestImagePickerNavigationHintsFitNarrowWindows(t *testing.T) {
	m := pickerForTest(t)
	m.settings.prompt = strings.Repeat("long prompt ", 100)
	for _, width := range []int{40, 44, 45, 80} {
		m.width, m.height = width, 12
		for _, focus := range []string{"prompt", "command"} {
			m.focus = focus
			lines := strings.Split(ansi.Strip(m.View().Content), "\n")
			footer := lines[len(lines)-1]
			require.Contains(t, footer, "Ctrl+C exit", "layout=%s width=%d focus=%s", "compact", width, focus)
			require.Contains(t, footer, "Enter ")
			if focus == "prompt" {
				require.Contains(t, footer, "↓ settings")
			} else {
				require.Contains(t, footer, "↑↓ scroll")
			}
			require.NotContains(t, footer, "…", "essential controls must fit without truncation")
		}
	}
}

func TestImagePickerLongPromptKeepsSettingChangesVisible(t *testing.T) {
	m := pickerForTest(t)
	m.settings.prompt = strings.Repeat("long prompt ", 1000)
	m.field = "quality"
	m.apply("max")
	m.field = "background"
	m.apply("transparent")
	lines, _, total := m.commandPreview(80, 4)
	require.Greater(t, total, len(lines))
	preview := strings.Join(lines, "")
	require.Contains(t, preview, "--quality max")
	require.Contains(t, preview, "--background transparent")
	require.Contains(t, preview, "--count 1")
	fullCommand := formatImagePickerCommandBash(m.settings.args())
	require.Contains(t, fullCommand, imagePickerShellQuote(m.settings.prompt))
}

func TestImagePickerCommandScrollPreservesEveryCharacter(t *testing.T) {
	for _, size := range [][2]int{{46, 12}, {86, 24}} {
		m := pickerForTest(t)
		prompt := strings.Repeat("'quoted' 雪 e\u0301  $HOME ", 30) + "\nend\x1b"
		m.settings.prompt, m.draft, m.cursor = prompt, []rune(prompt), len([]rune(prompt))
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		pickerKey(m, tea.KeyTab)
		pickerKey(m, tea.KeyTab)
		width, height := m.commandDimensions()
		visited := map[int]string{}
		total := 0
		for {
			lines, start, count := m.commandPreview(width, height)
			total = count
			for i, line := range lines {
				visited[start+i] = line
			}
			require.Contains(t, m.View().Content, "↑↓ scroll")
			require.NotContains(t, m.View().Content, "Generate image")
			require.NotContains(t, m.View().Content, "Print command")
			if start+len(lines) == count {
				break
			}
			pickerKey(m, tea.KeyPgDown)
		}
		require.Len(t, visited, total)
		var reconstructed strings.Builder
		for i := range total {
			reconstructed.WriteString(visited[i])
		}
		require.Equal(t, formatImagePickerCommandBash(m.settings.args()), reconstructed.String())
		require.Equal(t, "command", m.focus)
		require.NotNil(t, pickerKey(m, tea.KeyEnter))
		require.Equal(t, prompt, pickerArg(t, m.result.Args, "--prompt"))
	}
}

func TestImagePickerCommandScrollKeysResetAndResize(t *testing.T) {
	m := pickerForTest(t)
	m.insertPrompt(strings.Repeat(" long prompt", 100))
	m.focus = "command"
	width, height := m.commandDimensions()
	pickerKey(m, tea.KeyDown)
	require.Equal(t, 1, m.commandOffset)
	pickerKey(m, tea.KeyUp)
	require.Zero(t, m.commandOffset)
	pickerKey(m, tea.KeyPgDown)
	require.Equal(t, height, m.commandOffset)
	pickerKey(m, tea.KeyPgUp)
	require.Zero(t, m.commandOffset)
	pickerKey(m, tea.KeyEnd)
	require.Equal(t, len(m.commandLines(width))-height, m.commandOffset)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	width, height = m.commandDimensions()
	require.Equal(t, len(m.commandLines(width))-height, m.commandOffset)
	pickerKey(m, tea.KeyHome)
	require.Zero(t, m.commandOffset)
	pickerKey(m, tea.KeyEnd)
	m.focus = "prompt"
	before := m.commandOffset
	pickerKey(m, tea.KeyLeft)
	require.Equal(t, before, m.commandOffset, "cursor movement must preserve scrolling")
	m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	require.Zero(t, m.commandOffset)
	m.focus = "command"
	pickerKey(m, tea.KeyEnd)
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	pickerKey(m, tea.KeyEnter)
	pickerKey(m, tea.KeyDown)
	pickerKey(m, tea.KeyEnter)
	require.Zero(t, m.commandOffset, "changing a setting must reveal command changes")
}

func TestImagePickerPromptPasteIsText(t *testing.T) {
	m := pickerForTest(t)
	pickerKey(m, tea.KeyTab)
	m.Update(tea.PasteMsg{Content: "q\n\x03\x1b[B\r"})
	require.Empty(t, m.result.Args)
	require.False(t, m.result.Canceled)
	require.Equal(t, "settings", m.page)
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	paste := "'quoted' $HOME `touch /never` q\n\x03\x1b]52;c;evil\a\t雪"
	m.Update(tea.PasteMsg{Content: paste})
	require.Equal(t, paste, string(m.draft))
	require.Equal(t, "prompt", m.focus)
	require.False(t, m.result.Canceled)
	m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	require.Equal(t, paste+"q", string(m.draft))
	require.False(t, m.result.Canceled)
	require.Equal(t, paste+"q", m.settings.prompt)
	require.Contains(t, formatImagePickerCommandBash(m.settings.args()), "\\x1b")
	view := m.View().Content
	require.NotContains(t, view, "\x1b]")
	require.NotContains(t, view, "\x03")
	m.Update(tea.PasteMsg{Content: "\n\x1b[B\n"})
	require.Empty(t, m.result.Args)
	require.Equal(t, paste+"q\n\x1b[B\n", m.settings.prompt)
}

func TestImagePickerPromptEditing(t *testing.T) {
	m := pickerForTest(t)
	m.settings.prompt, m.draft, m.cursor = "A雪B", []rune("A雪B"), 3
	pickerKey(m, tea.KeyLeft)
	pickerKey(m, tea.KeyBackspace)
	require.Equal(t, "AB", string(m.draft))
	m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	pickerKey(m, tea.KeyHome)
	pickerKey(m, tea.KeyDelete)
	pickerKey(m, tea.KeyEnd)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
	require.Nil(t, cmd)
	require.Empty(t, m.result.Args)
	require.Equal(t, "qB\n", m.settings.prompt)
}

func TestImagePickerCancellationEveryPage(t *testing.T) {
	for _, page := range []string{"settings", "choose", "more"} {
		t.Run(page, func(t *testing.T) {
			m := pickerForTest(t)
			m.page, m.field = page, "model"
			_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
			require.NotNil(t, cmd)
			require.True(t, m.result.Canceled)
			require.Empty(t, m.result.Args)
		})
	}
}

func TestImagePickerAllRowsVisibleWhenSelected(t *testing.T) {
	for _, page := range []string{"settings", "choose", "more"} {
		m := pickerForTest(t)
		m.page, m.field, m.width, m.height = page, "count", 48, 12
		m.focus = "options"
		for i, row := range m.rows() {
			m.selected = i
			view := m.View()
			require.False(t, view.AltScreen)
			require.Contains(t, ansi.Strip(view.Content), "› "+row.label, "layout=%s page=%s row=%d", "compact", page, i)
		}
	}
}

func TestImagePickerViewBoundsAndResize(t *testing.T) {
	m := pickerForTest(t)
	m.settings.prompt = strings.Repeat("雪 e\u0301 👨‍👩‍👧‍👦 \n\t\x1b[31m ", 1000)
	for _, size := range [][2]int{{0, 0}, {1, 1}, {12, 3}, {39, 11}, {40, 12}, {48, 12}, {80, 24}, {240, 80}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, page := range []string{"settings", "choose", "more"} {
			m.page, m.field, m.selected = page, "quality", 0
			m.draft = []rune(m.settings.prompt)
			m.cursor = len(m.draft)
			content := m.View().Content
			if size[1] == 0 {
				require.Empty(t, content)
				continue
			}
			lines := strings.Split(content, "\n")
			require.LessOrEqual(t, len(lines), size[1], "page=%s", page)
			require.LessOrEqual(t, len(lines), 18, "page=%s", page)
			if size[1] >= 12 {
				require.LessOrEqual(t, len(lines), size[1]-3, "leave prior shell context visible")
			}
			for _, line := range lines {
				require.LessOrEqual(t, ansi.StringWidth(line), size[0], "page=%s size=%v", page, size)
			}
			require.NotContains(t, content, "\x1b[31m")
		}
	}
}

func TestImagePickerInlineFinishedView(t *testing.T) {
	for _, action := range []tea.Msg{
		tea.KeyPressMsg{Code: tea.KeyEnter},
		tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl},
		tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl},
		imagePickerStopMsg{code: 143},
	} {
		m := pickerForTest(t)
		view := m.View()
		require.False(t, view.AltScreen)
		require.NotEmpty(t, view.Content)
		_, cmd := m.Update(action)
		require.NotNil(t, cmd)
		view = m.View()
		require.False(t, view.AltScreen)
		require.Empty(t, view.Content, "the final frame must erase the inline controls")
	}
}

func TestImagePickerInlineRestoresModes(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "output")
	require.NoError(t, err)
	defer f.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &imagePickerOutput{File: f, cancel: cancel}
	p := &imagePickerInline{output: w, restoreWrap: true}
	p.start()
	p.start() // Repeated capability replies must not start another lifecycle.
	p.close()
	p.close()
	require.NoError(t, w.Err())
	require.NoError(t, ctx.Err())
	_, err = f.Seek(0, io.SeekStart)
	require.NoError(t, err)
	output, err := io.ReadAll(f)
	require.NoError(t, err)
	require.Equal(t, ansi.ResetModeTextCursorEnable+ansi.SetModeBracketedPaste+ansi.SetModeAutoWrap+
		"\r"+ansi.EraseScreenBelow+ansi.ResetModeBracketedPaste+ansi.SetModeTextCursorEnable+ansi.ResetModeAutoWrap, string(output))
}

func TestImagePickerEarlyPasteCannotSubmitBeforeFirstPaint(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "output")
	require.NoError(t, err)
	defer f.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, err := newImagePicker(imagePickerOptions{})
	require.NoError(t, err)
	m.width, m.height = 80, 24
	p := &imagePickerInline{model: m, output: &imagePickerOutput{File: f, cancel: cancel}}
	require.NotNil(t, p.Init())
	prompt := "Early paste\nq 'quoted'\rfinal line"
	_, cmd := p.Update(tea.PasteMsg{Content: prompt})
	require.Nil(t, cmd)
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyEnter}, {Code: 'g', Mod: tea.ModCtrl}, {Code: 'p', Mod: tea.ModCtrl},
	} {
		_, cmd := p.Update(key)
		require.Nil(t, cmd)
	}
	require.Equal(t, prompt, m.settings.prompt)
	require.Equal(t, prompt, string(m.draft))
	require.Empty(t, m.result.Args)
	require.False(t, m.result.Canceled)
	_, cmd = p.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	p.close()
	require.NoError(t, ctx.Err())
	_, err = f.Seek(0, io.SeekStart)
	require.NoError(t, err)
	output, err := io.ReadAll(f)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(output), ansi.ResetModeTextCursorEnable+ansi.SetModeBracketedPaste))
	require.Contains(t, string(output), ansi.ResetModeBracketedPaste+ansi.SetModeTextCursorEnable)
	require.NotContains(t, string(output), ansi.EraseScreenBelow, "no frame was painted or owned")
}

func TestImagePickerInlineFrameHasOneLogicalLine(t *testing.T) {
	for _, width := range []int{1, 7, 40, 80, 120} {
		content := strings.Repeat("❤️", 10) + "\n\x1b[31m" + strings.Repeat("1️⃣", 10) + "\x1b[m\n雪 e\u0301 👨‍👩‍👧‍👦"
		frame := imagePickerInlineFrame(content, width)
		require.NotContains(t, frame, "\n", "hard line breaks lose ownership during reflow")
		require.True(t, strings.HasPrefix(frame, "\r"+ansi.EraseScreenBelow))
		require.True(t, strings.HasSuffix(frame, "\r"+ansi.CursorUp(2)))
		paint := strings.TrimSuffix(strings.TrimPrefix(frame, "\r"+ansi.EraseScreenBelow), "\r"+ansi.CursorUp(2))
		rows := strings.Split(paint, ansi.CursorHorizontalAbsolute(width)+" ")
		require.Len(t, rows, 4, "three explicit right-edge wrap boundaries")
		require.Empty(t, rows[3])
		for _, row := range rows[:3] {
			require.True(t, strings.HasPrefix(row, " \r"))
			require.Less(t, ansi.WcWidth.StringWidth(strings.TrimPrefix(row, " \r")), width)
			require.Less(t, ansi.StringWidth(strings.TrimPrefix(row, " \r")), width)
		}
	}
	require.Equal(t, "\r"+ansi.EraseScreenBelow, imagePickerInlineFrame("", 80))
}

func TestImagePickerWidthUsesWholeEmojiClusters(t *testing.T) {
	for _, test := range []struct {
		text  string
		cells int
	}{{"1️⃣", 2}, {"❤️", 2}, {"👨‍👩‍👧‍👦", 8}, {"雪", 2}, {"e\u0301", 1}} {
		sequence, cells, read := imagePickerSequence(test.text + "next")
		require.Equal(t, test.text, sequence)
		require.Equal(t, test.cells, cells, test.text)
		require.Equal(t, len(test.text), read)
	}
}

func TestImagePickerParentContextDuringInput(t *testing.T) {
	if descriptor := os.Getenv("OPENAI_PICKER_CONTEXT_TEST_FD"); descriptor != "" {
		fd, err := strconv.Atoi(descriptor)
		require.NoError(t, err)
		control := os.NewFile(uintptr(fd), "context-control")
		defer control.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			var data [1]byte
			_, _ = control.Read(data[:])
			cancel()
		}()
		result, err := runImagePicker(ctx, os.Stdin, os.Stdout, imagePickerOptions{Prompt: "Synthetic context cancellation"})
		require.ErrorIs(t, err, context.Canceled)
		require.True(t, result.Canceled)
		require.Empty(t, result.Args)
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("requires a Unix PTY")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is needed for the PTY regression")
	}
	const script = `import errno,fcntl,os,pty,select,struct,subprocess,sys,termios,time
master,slave=pty.openpty()
fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',24,80,0,0))
initial=termios.tcgetattr(slave)
control,writer=os.pipe()
env=dict(os.environ,TERM='xterm-256color',NO_COLOR='1',OPENAI_PICKER_CONTEXT_TEST_FD=str(control))
child=subprocess.Popen([sys.argv[1],'-test.run=^TestImagePickerParentContextDuringInput$'],stdin=slave,stdout=slave,stderr=slave,env=env,pass_fds=(control,))
os.close(control)
raw=bytearray()
deadline=time.monotonic()+12
sent=False
try:
    while child.poll() is None:
        assert time.monotonic()<deadline, 'context cancellation did not terminate picker'
        if select.select([master],[],[],.05)[0]:
            raw.extend(os.read(master,65536))
        if not sent and b'openai images generate' in raw:
            assert not termios.tcgetattr(slave)[3]&(termios.ICANON|termios.ECHO), 'input is not raw'
            os.write(writer,b'x')
            sent=True
    while select.select([master],[],[],.05)[0]:
        raw.extend(os.read(master,65536))
    assert sent, raw
    assert child.returncode==0, raw
    restored=termios.tcgetattr(slave)
    if sys.platform=='darwin':
        initial[3]&=~termios.PENDIN
        restored[3]&=~termios.PENDIN
    assert restored==initial, (initial,restored)
    assert b'\x1b[?2004h' in raw and b'\x1b[?2004l' in raw, raw
    assert b'\x1b[?25h' in raw, raw
    assert b'\x1b[?1049h' not in raw, raw
finally:
    if child.poll() is None:
        child.kill()
        child.wait()
    os.close(writer)
    os.close(master)
    os.close(slave)
`
	command := exec.Command(python, "-I", "-B", "-c", script, os.Args[0])
	out, err := command.CombinedOutput()
	require.NoError(t, err, "%s", out)
}

func TestImagePickerRejectsUnrepresentablePrompts(t *testing.T) {
	for _, prompt := range []string{"", " \n\t", "a\x00b", `\@literal`} {
		m := pickerForTest(t)
		m.settings.prompt = prompt
		require.Nil(t, pickerKey(m, tea.KeyEnter))
		require.Equal(t, "settings", m.page)
		require.NotEmpty(t, m.note)
		require.Empty(t, m.result.Args)
	}
}

func TestImagePickerFileLookingPromptStaysLiteral(t *testing.T) {
	for _, prompt := range []string{"@file:///does/not/exist", "@/dev/stdin", "@data://missing", "null", "'quotes'"} {
		m := pickerForTest(t)
		m.settings.prompt = prompt
		args := m.settings.args()
		body, err := embedFiles(map[string]any{"prompt": pickerArg(t, args, "--prompt")}, EmbedText, &onceStdinReader{})
		require.NoError(t, err)
		require.Equal(t, prompt, body.(map[string]any)["prompt"])
	}
}

func TestImagePickerNativeShellRoundTrip(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			path, err := exec.LookPath(shell)
			if err != nil {
				t.Skipf("%s unavailable", shell)
			}
			args := []string{"images", "generate", "--prompt", "  'single' \"double\" $(exit 9) `exit 8` $HOME\\*;\nnext\tline\r\x1b]52;c;evil\a \u0085\u202e 雪  ", "--model", "gpt-image-2.5-sunburst", ""}
			command := formatImagePickerCommandBash(args)
			for _, r := range command {
				require.False(t, unicode.IsControl(r) || unicode.Is(unicode.Cf, r))
			}
			// Replace openai with an in-shell argument recorder; no child CLI or API.
			script := "openai() { printf '%s\\0' \"$@\"; }; " + command
			out, err := exec.Command(path, "-c", script).Output()
			require.NoError(t, err)
			require.Equal(t, args, strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00"))
		})
	}
}

func TestImagePickerRefusesPipesWithoutWriting(t *testing.T) {
	in, inputWriter, err := os.Pipe()
	require.NoError(t, err)
	defer in.Close()
	defer inputWriter.Close()
	outReader, out, err := os.Pipe()
	require.NoError(t, err)
	defer outReader.Close()
	_, err = runImagePickerSession(context.Background(), in, out, imagePickerOptions{Prompt: "synthetic"})
	require.ErrorContains(t, err, "terminal input and output")
	require.NoError(t, out.Close())
	data, err := io.ReadAll(outReader)
	require.NoError(t, err)
	require.Empty(t, data)
}

func TestImagePickerOutputFailureCancels(t *testing.T) {
	for _, useString := range []bool{false, true} {
		f, err := os.CreateTemp(t.TempDir(), "output")
		require.NoError(t, err)
		require.NoError(t, f.Close())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		w := &imagePickerOutput{File: f, cancel: cancel}
		if useString {
			_, err = io.WriteString(w, "frame")
		} else {
			_, err = w.Write([]byte("frame"))
		}
		require.Error(t, err)
		require.True(t, errors.Is(w.Err(), os.ErrClosed))
		require.ErrorIs(t, ctx.Err(), context.Canceled)
	}
}

func formatImagePickerCommandBash(args []string) string {
	return formatImagePickerCommand(args, "bash")
}
