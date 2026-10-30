package audio

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestAudioCopyCommandKeepsInspectorAndCopiesOnlyRunnableBytes(t *testing.T) {
	for _, tc := range []struct {
		name, notice string
		confirmed    bool
		err          error
	}{
		{"confirmed", "Command copied.", true, nil},
		{"requested", "Copy requested. Ctrl+P prints.", false, nil},
		{"failed", "Copy failed. Ctrl+P prints.", false, errors.New("synthetic-private-diagnostic")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OPENAI_API_KEY", "sk-fake-test")
			m := audioPreviewModel(t, "'/example/CLI with spaces/openai'", "--project=synthetic")
			m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
			m.input.SetValue(strings.Repeat("世界's two  spaces. ", 30))
			m.instructions.SetValue("Newline\nKeep  quoted text.")
			raw, ok := m.rawCommand()
			require.True(t, ok)
			require.NotContains(t, raw, "sk-fake-test")
			require.NotContains(t, raw, "\x1b")
			var writes []string
			m.clipboard.write = func(_ context.Context, text string) (bool, error) {
				writes = append(writes, text)
				return tc.confirmed, tc.err
			}
			m.focusCommand()
			require.Nil(t, audioReviewKey(m, tea.KeyEnd))
			offset := m.commandOffset
			require.Positive(t, offset)
			require.Empty(t, writes)
			for _, key := range "cC" {
				command := audioReviewKey(m, key)
				require.NotNil(t, command)
				require.Nil(t, audioReviewKey(m, key), "ignore repeated keys while a copy is pending")
				m.Update(command())
				view := ansi.Strip(m.View().Content)
				require.Contains(t, view, tc.notice)
				require.NotContains(t, view, "synthetic-private-diagnostic")
				require.False(t, m.quit)
				require.Equal(t, audioFocusCommand, m.focus)
				require.Equal(t, offset, m.commandOffset)
				require.Empty(t, m.commandToPrint)
				require.False(t, m.busy)
			}
			require.Equal(t, []string{raw, raw}, writes)
		})
	}
}

func TestAudioCopyCommandRespectsPrivateOverridesAndInvalidSpeed(t *testing.T) {
	for _, tc := range []struct{ name, flag string }{
		{"private", "--header=Authorization: Bearer synthetic-private-header"},
		{"invalid-speed", "--project=synthetic"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := audioPreviewModel(t, "openai", tc.flag)
			m.clipboard.write = func(context.Context, string) (bool, error) {
				t.Fatal("blocked draft reached clipboard")
				return false, nil
			}
			if tc.name == "invalid-speed" {
				m.speedEditing = true
				m.speedInput.SetValue("4.01")
			}
			m.focusCommand()
			require.Nil(t, audioReviewKey(m, 'c'))
			require.Empty(t, m.clipboard.notice)
			view := ansi.Strip(m.View().Content)
			require.NotContains(t, view, "synthetic-private-header")
			require.NotContains(t, view, "c copy")
		})
	}
}

func TestAudioRemoteCommandCopyUsesOnlyEncodedClipboardBytes(t *testing.T) {
	t.Setenv("SSH_TTY", "/dev/synthetic-terminal")
	var writes []string
	write := audioClipboardWriter(func(s string) { writes = append(writes, s) })
	raw := "printf %s \\\n  '世界' | openai audio speak --output speech.wav"
	confirmed, err := write(t.Context(), raw)
	require.NoError(t, err)
	require.False(t, confirmed)
	require.Equal(t, []string{ansi.SetSystemClipboard(raw)}, writes)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = write(ctx, raw)
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, writes, 1)
}
