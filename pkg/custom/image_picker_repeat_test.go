package custom

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestImagePickerResumesWithSettingsAndEmptyPrompt(t *testing.T) {
	original := pickerForTest(t).settings
	original.prompt = "Previous synthetic prompt"
	original.quality, original.outputDir = "high", t.TempDir()
	m, err := newImagePicker(imagePickerOptions{initial: &original, resuming: true})
	require.NoError(t, err)
	m.width, m.height = 80, 24
	require.Equal(t, "settings", m.page)
	require.Equal(t, "prompt", m.focus)
	want := original
	want.prompt = ""
	require.Equal(t, want, m.settings)
	require.Empty(t, m.draft)
	require.Zero(t, m.cursor)
	require.Contains(t, ansi.Strip(m.View().Content), "Describe your image")
	require.NotContains(t, ansi.Strip(m.View().Content), original.prompt)
	require.Contains(t, ansi.Strip(m.View().Content), "Ctrl+C")
	require.Empty(t, m.result.Args, "reopening alone must never submit")
	pickerKey(m, tea.KeyEnter)
	require.Empty(t, m.result.Args, "a new description is required")
	require.Equal(t, "Add a prompt first.", m.note)
	require.Equal(t, "Previous synthetic prompt", original.prompt, "reopening must not mutate the submitted selection")
}

func TestImagePickerResumeIgnoresQueuedAndRepeatingSubmit(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "terminal")
	require.NoError(t, err)
	defer file.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, err := newImagePicker(imagePickerOptions{Prompt: "previous", resuming: true})
	require.NoError(t, err)
	p := &imagePickerInline{model: m, output: &imagePickerOutput{File: file, cancel: cancel}, resuming: true}
	for _, msg := range []tea.Msg{
		tea.KeyPressMsg{Code: 'n'}, tea.KeyPressMsg{Code: tea.KeyEnter},
		tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}, tea.PasteMsg{Content: "new\n"},
	} {
		_, cmd := p.Update(msg)
		require.Nil(t, cmd)
		require.Equal(t, "previous", string(m.draft))
		require.Empty(t, m.result.Args)
	}
	p.start()
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyEnter}, {Code: 'g', Mod: tea.ModCtrl}, {Code: 'p', Mod: tea.ModCtrl},
	} {
		p.submitAfter = time.Now().Add(time.Minute)
		before := time.Now()
		_, cmd := p.Update(key)
		require.Nil(t, cmd, "blocked keys must not queue delayed submissions")
		require.Empty(t, m.result.Args)
		require.False(t, p.submitAfter.Before(before.Add(imagePickerResumeQuiet)))
	}
	// Cancellation remains available even before the first paint.
	p.started = false
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	require.True(t, m.result.Canceled)
	require.NoError(t, ctx.Err())
}

func TestImagePickerRepeatedActionsRestoreFlagsOnFailure(t *testing.T) {
	failure := errors.New("second request failed")
	app := &cli.Command{Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "prompt", BodyPath: "prompt"},
		&cli.StringFlag{Name: "output-dir"},
	}}
	app.Action = func(ctx context.Context, command *cli.Command) error {
		before := requestflag.ExtractRequestContents(command)
		for i, pair := range [][2]string{{"first", "/first folder"}, {"second", "/second folder"}} {
			calls := 0
			err := runImagePickerAction(ctx, command, []string{"images", "generate", "--prompt", pair[0], "--output-dir", pair[1]}, func(context.Context, *cli.Command) error {
				calls++
				require.Equal(t, pair[0], command.String("prompt"))
				require.Equal(t, pair[1], command.String("output-dir"))
				if i == 1 {
					return failure
				}
				return nil
			})
			if i == 1 {
				require.ErrorIs(t, err, failure)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, 1, calls)
			require.Equal(t, before, requestflag.ExtractRequestContents(command))
			require.False(t, command.IsSet("prompt"))
			require.False(t, command.IsSet("output-dir"))
			require.Empty(t, command.String("output-dir"))
		}
		return nil
	}
	require.NoError(t, app.Run(context.Background(), []string{"openai"}))
}
