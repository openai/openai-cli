package audio

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestAudioEditorPastesWindowsLinesOnce(t *testing.T) {
	m := newAudioWorkbench(t.Context(), &cli.Command{Name: "openai"}, func(context.Context, *cli.Command) error { return nil }, audiohistory.Store{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.PasteMsg{Content: "@literal\r\nSecond 世界\rThird"})
	require.Equal(t, "@literal\nSecond 世界\nThird", m.input.Value())
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m.Update(tea.PasteMsg{Content: "\\@instructions\r\nDelivery"})
	require.Equal(t, audioInstructionsExample+"\\@instructions\nDelivery", m.instructions.Value())
}

func TestAudioEditorRestoresOneNewlinePerWindowsLine(t *testing.T) {
	m := newAudioWorkbench(t.Context(), &cli.Command{Name: "openai"}, nil, audiohistory.Store{})
	settings := audioDefaultSettings()
	settings.Input, settings.Instructions = "First\r\nSecond", "Warm\r\nSlow"
	m.Update(audioWorkMessage{kind: "open", result: audioWorkResult{entry: audiohistory.Entry{ID: "synthetic", Settings: settings}}})
	require.Equal(t, "First\nSecond", m.input.Value())
	require.Equal(t, "Warm\nSlow", m.instructions.Value())
	require.Equal(t, "First\r\nSecond", m.result.entry.Settings.Input, "restoring a draft changed the retained request")
}

func TestAudioEditorAcceptsExactSpeedWithoutGeneration(t *testing.T) {
	m := newAudioWorkbench(t.Context(), &cli.Command{Name: "openai"}, nil, audiohistory.Store{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.focus, m.setting = 2, 3
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "speed", m.modal)
	for _, value := range []string{"NaN", "+Inf", "0.24", "4.01", "invalid"} {
		m.speedInput.SetValue(value)
		_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		require.Nil(t, cmd)
		require.Equal(t, "speed", m.modal)
		require.Equal(t, float64(1), m.settings.Speed)
	}
	m.speedInput.SetValue("1.23")
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, cmd)
	require.Empty(t, m.modal)
	require.Empty(t, m.note)
	require.False(t, m.busy)
	require.Equal(t, 1.23, m.settings.Speed)
}

func TestAudioEditorNarrowValidationRemainsVisible(t *testing.T) {
	m := newAudioWorkbench(t.Context(), &cli.Command{Name: "openai"}, nil, audiohistory.Store{})
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	require.Contains(t, m.View().Content, "Enter text before generating speech.")
	require.False(t, m.busy)
}

func TestAudioEditorSaveValidationRemainsVisible(t *testing.T) {
	for _, width := range []int{40, 90} {
		m := audioReviewModel(t)
		m.Update(tea.WindowSizeMsg{Width: width, Height: 12})
		result := audioReviewResult("synthetic", false)
		m.result, m.focus, m.note = &result, 4, "Previous result status"
		m.Update(tea.KeyPressMsg{Code: 's'})
		require.Equal(t, "save", m.modal)
		require.NotContains(t, m.View().Content, "Previous result status")
		for _, value := range []string{"", "-", "/dev/stdout"} {
			m.path.SetValue(value)
			_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			require.Nil(t, cmd)
			require.Equal(t, "save", m.modal)
			require.Equal(t, value, m.path.Value())
			require.Contains(t, m.View().Content, "Enter a file path for saving.")
			require.False(t, m.busy)
		}
	}
}
