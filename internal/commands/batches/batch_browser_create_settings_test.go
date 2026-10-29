package batches

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestBatchBrowserCreateFileHintSpacing(t *testing.T) {
	_, err := (workflow{}).createBatchInBrowser(t.Context(), &cli.Command{}, openai.Client{}, nil, newBatchBrowserDraft(), func(_ context.Context, m *batchBrowserScreen) error {
		m.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
		lines := strings.Split(ansi.Strip(m.View().Content), "\n")
		var input int
		for i, line := range lines {
			if strings.Contains(line, "Browse or type a file path") {
				input = i
			}
		}
		require.Positive(t, input)
		valueColumn := strings.Index(lines[input], "Browse or type")
		require.Empty(t, strings.Trim(lines[input+1], " │"))
		require.Equal(t, valueColumn, strings.Index(lines[input+2], "One prepared"))
		require.Equal(t, valueColumn, strings.Index(lines[input+3], "Turn each"))
		require.Empty(t, strings.Trim(lines[input+4], " │"))
		require.Contains(t, lines[input+5], "Options")

		for range 3 {
			m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		}
		for _, size := range []tea.WindowSizeMsg{{Width: 50, Height: 14}, {Width: 30, Height: 7}} {
			m.Update(size)
			view := ansi.Strip(m.View().Content)
			require.Contains(t, view, "> Output expiration")
			require.Contains(t, view, "Esc")
			rows := strings.Split(view, "\n")
			require.LessOrEqual(t, len(rows), size.Height)
			for _, row := range rows {
				require.LessOrEqual(t, ansi.StringWidth(row), size.Width)
			}
		}
		m.chosen = "back"
		return nil
	})
	require.NoError(t, err)
}

func TestBatchBrowserSettingsStayOnCreateForm(t *testing.T) {
	draft := newBatchBrowserDraft()
	draft.inputID, draft.metadata = "file_test", `{"old":"preserved"}`
	screenCount := 0
	_, err := (workflow{}).createBatchInBrowser(t.Context(), &cli.Command{}, openai.Client{}, nil, draft, func(_ context.Context, m *batchBrowserScreen) error {
		screenCount++
		require.Equal(t, "Create batch", m.title)
		m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
		key := func(code rune) {
			m.Update(tea.KeyPressMsg{Code: code})
			require.False(t, m.closed, "editing settings must not close the form")
		}
		at := func(value string) {
			require.Equal(t, value, m.choices[m.visible[m.index]].value)
		}
		at("input")
		key(tea.KeyDown)
		at("review")
		key(tea.KeyDown)
		at("endpoint")
		key(tea.KeyDown)
		at("metadata")
		key(tea.KeyEnter)
		require.Equal(t, draft.metadata, m.input.Value())
		m.input.SetValue(`{"bad":null}`)
		key(tea.KeyEnter)
		require.Contains(t, m.notice, "text values")
		at("metadata")
		key(tea.KeyEsc)
		require.Equal(t, `{"old":"preserved"}`, draft.metadata)
		m.Update(tea.KeyPressMsg{Code: '{', Text: "{"})
		require.Equal(t, "{", m.input.Value())
		m.Update(tea.PasteMsg{Content: `"source":"demo"}`})
		key(tea.KeyEnter)
		require.Equal(t, `{"source":"demo"}`, draft.metadata)
		m.Update(tea.PasteMsg{Content: `{"replace":"discard"}`})
		require.Equal(t, `{"replace":"discard"}`, m.input.Value())
		key(tea.KeyEsc)
		require.Equal(t, `{"source":"demo"}`, draft.metadata)
		key(tea.KeyDown)
		at("expires")
		key(tea.KeyEnter)
		at("0")
		key(tea.KeyDown)
		key(tea.KeyEnter)
		at("expires")
		require.EqualValues(t, 3600, draft.expires)
		key(tea.KeyEnter)
		at("3600")
		for i := 0; i < 4; i++ {
			key(tea.KeyDown)
		}
		at("custom")
		key(tea.KeyEnter)
		m.input.SetValue("100")
		key(tea.KeyEnter)
		require.Contains(t, m.notice, "3600")
		key(tea.KeyEsc)
		require.Equal(t, "expires", m.flow.setting)
		require.EqualValues(t, 3600, draft.expires)
		key(tea.KeyEnter)
		m.input.SetValue("7200")
		key(tea.KeyEnter)
		at("expires")
		require.Equal(t, "7200 seconds", m.choices[m.index].setting)
		body, err := draft.body()
		require.NoError(t, err)
		require.Equal(t, "demo", gjson.GetBytes(body, "metadata.source").Str)
		require.EqualValues(t, 7200, gjson.GetBytes(body, "output_expires_after.seconds").Int())
		require.Equal(t, "created_at", gjson.GetBytes(body, "output_expires_after.anchor").Str)
		key(tea.KeyEnter)
		at("custom")
		key(tea.KeyEsc)
		at("expires")
		key(tea.KeyEnter)
		for i := 0; i < 5; i++ {
			key(tea.KeyUp)
		}
		at("0")
		key(tea.KeyEnter)
		require.Zero(t, draft.expires)
		key(tea.KeyUp)
		at("metadata")
		key(tea.KeyEnter)
		m.input.Reset()
		key(tea.KeyEnter)
		require.Empty(t, draft.metadata)
		require.Equal(t, "None", m.choices[m.index].setting)
		m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
		require.Equal(t, "back", m.chosen)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, screenCount)
}
