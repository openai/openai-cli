package audio

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/stretchr/testify/require"
)

func TestAudioViewsDistinguishKeysFromDescriptions(t *testing.T) {
	cases := []struct {
		name, key, action string
		setup             func(*audioWorkbench)
	}{
		{"form", "[Ctrl+C]", "Exit", func(m *audioWorkbench) {}},
		{"too-small", "Ctrl+C", "exits.", func(m *audioWorkbench) { m.Update(tea.WindowSizeMsg{Width: 39, Height: 11}) }},
		{"settings", "[Tab]", "Audio", func(m *audioWorkbench) { m.focusSection(2) }},
		{"more", "[Esc]", "Back", func(m *audioWorkbench) { m.modal = "more" }},
		{"speed", "Enter", "apply", func(m *audioWorkbench) { m.modal = "speed" }},
		{"choices", "Enter", "select", func(m *audioWorkbench) {
			m.modal, m.setting, m.choices = "choice", 0, audioModels
		}},
		{"history", "Enter", "open", func(m *audioWorkbench) {
			m.modal, m.entries = "history", []audiohistory.Entry{m.result.entry}
		}},
		{"history-empty", "Esc", "back", func(m *audioWorkbench) { m.modal = "history" }},
		{"player", "←→", "choose", func(m *audioWorkbench) { m.focusSection(4) }},
		{"player-playing", "Space", "stop", func(m *audioWorkbench) {
			m.focusSection(4)
			m.playing = true
		}},
		{"player-finished", "Space", "play", func(m *audioWorkbench) {
			m.focusSection(4)
			m.result.finished = true
		}},
		{"player-unmeasured", "Space", "play", func(m *audioWorkbench) {
			m.focusSection(4)
			m.result.measured = false
			m.result.analysis.Duration = 0
		}},
		{"player-save", "Enter", "save", func(m *audioWorkbench) {
			m.focusSection(4)
			m.resultAction = 1
		}},
		{"save", "Tab", "edit", func(m *audioWorkbench) { m.openSave() }},
		{"save-filename", "Enter", "save", func(m *audioWorkbench) { m.openSave(); m.focusSave(0) }},
		{"save-folder", "Enter", "done", func(m *audioWorkbench) { m.openSave(); m.focusSave(1) }},
		{"error-details", "↑↓", "scroll", func(m *audioWorkbench) { m.modal = "error" }},
		{"help", "↑↓", "scroll", func(m *audioWorkbench) {
			m.modal, m.infoTitle, m.infoText = "info", "Help", audioFormHelp
		}},
		{"command", "Ctrl+P", "print/exit", func(m *audioWorkbench) {
			m.modal, m.infoTitle, m.infoText = "info", audioCommandTitle, m.commandPreview()
		}},
	}
	for _, size := range [][2]int{{40, 12}, {90, 34}} {
		for _, dark := range []bool{false, true} {
			accent, secondary := "49;89;188", "101;112;135"
			if dark {
				accent, secondary = "138;168;255", "164;172;194"
			}
			for _, tc := range cases {
				if size[0] == 40 && (tc.name == "form" || tc.name == "settings") {
					continue // Compact forms intentionally omit the shortcut row.
				}
				t.Run(fmt.Sprintf("%s/%dx%d/dark_%t", tc.name, size[0], size[1], dark), func(t *testing.T) {
					m := audioReviewModel(t)
					r := audioReviewResult("synthetic-color-check", false)
					m.result = &r
					m.input.SetValue("Hello. The train leaves at five.")
					m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
					tc.setup(m)
					m.resizeEditors()
					m.color, m.dark = true, dark
					view := m.View().Content
					if size[0] == 90 && dark && (tc.name == "form" || tc.name == "player") {
						t.Logf("Rendered audio %s:\n%s", tc.name, view)
					}
					require.Contains(t, view, "\x1b[38;2;"+accent+"m"+tc.key+"\x1b[m", "keys need regular-weight theme accents")
					require.Contains(t, view, "\x1b[38;2;"+secondary+"m"+tc.action+"\x1b[m", "descriptions must stay secondary")
					for _, row := range strings.Split(view, "\n") {
						require.LessOrEqual(t, ansi.StringWidth(row), size[0]-2)
					}
					require.LessOrEqual(t, len(strings.Split(view, "\n")), size[1]-1)
					m.color = false
					plain := m.View().Content
					require.NotContains(t, plain, "\x1b[38;")
					require.NotContains(t, plain, "\x1b[48;")
					require.Equal(t, ansi.Strip(view), ansi.Strip(plain), "color must not alter text or geometry")
				})
			}
		}
	}
}

func TestAudioBusyViewsKeepCancellationDistinctFromProgress(t *testing.T) {
	for _, focus := range []int{0, 2, 4} {
		m := audioReviewModel(t)
		r := audioReviewResult("synthetic-progress", false)
		m.result = &r
		m.focusSection(focus)
		m.setting = 4
		m.color, m.dark, m.busy = true, true, true
		m.started = time.Now()
		view := m.View().Content
		var progress string
		for _, row := range strings.Split(view, "\n") {
			if strings.Contains(row, "Working…") {
				progress = row
			}
		}
		require.NotEmpty(t, progress)
		require.Contains(t, progress, "\x1b[38;2;138;168;255mCtrl+C\x1b[m")
		require.Contains(t, progress, "\x1b[38;2;164;172;194mWorking…")
		require.Contains(t, progress, "\x1b[38;2;164;172;194mcancels\x1b[m")
		withoutMarker := strings.ReplaceAll(progress, "\x1b[1;38;2;138;168;255m› \x1b[m", "")
		require.NotContains(t, withoutMarker, "\x1b[1;", "only the existing focus marker may be bold")
	}
}
