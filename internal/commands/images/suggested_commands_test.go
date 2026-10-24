package images

import (
	"bytes"
	"context"
	"errors"
	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/commandsuggest"
	"github.com/openai/openai-cli/internal/imagemodels"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
	"io"
	"testing"
)

type recordedCommand struct {
	output      io.Writer
	shell, text string
}
type commandRecorder []recordedCommand

func (r *commandRecorder) Record(output io.Writer, shell, text string) {
	*r = append(*r, recordedCommand{output, shell, text})
}
func TestImageInlineSuggestsPreviewHelpWithoutPlaceholder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("APPDATA", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	var output bytes.Buffer
	root := &cli.Command{Name: "openai", Writer: &output, Commands: []*cli.Command{
		{Name: "images", Commands: []*cli.Command{{Name: "inline", Commands: imageInlinePreferenceCommands(func(context.Context, string) error { t.Fatal("suggested receipt was not used"); return nil }, func(ctx context.Context, command *cli.Command, text string, suggestions []string) error {
			presenter := clihelp.NewSuggestedText(&output, command)
			for _, suggestion := range suggestions {
				clihelp.SuggestedCommand(command, []io.Writer{presenter}, suggestion)
			}
			return presenter.WriteText(text)
		})}}},
	}}
	var recorder commandRecorder
	commandsuggest.SetRecorder(root, &recorder)
	require.NoError(t, root.Run(t.Context(), []string{"openai", "images", "inline", "off"}))
	require.Contains(t, output.String(), "Image preview help: openai images preview --help")
	require.Len(t, recorder, 1)
	require.Equal(t, "openai images preview --help", recorder[0].text)
}
func TestSuggestedImageModelCommandsReflectShownChoices(t *testing.T) {
	root := &cli.Command{Name: "openai"}
	var recorder commandRecorder
	commandsuggest.SetRecorder(root, &recorder)
	report := imageModelsReport{Source: "offline", Models: []imagemodels.Result{
		imageModelTestRow("gpt-image-1", imagemodels.StatusNotChecked, ""),
		imageModelTestRow("retired", imagemodels.StatusRetired, ""),
	}}
	var out, hints bytes.Buffer
	require.NoError(t, writeImageModels(&out, report, false, "openai", &hints, root))
	require.Equal(t, commandRecorder{
		{&hints, "", "openai images models --all"},
		{&hints, "", `openai images generate --prompt "A tiny orange robot" --model gpt-image-1`},
	}, recorder)
	require.NotContains(t, out.String(), "Choose a model:")
	require.Contains(t, hints.String(), "Choose a model:")
	recorder = nil
	require.NoError(t, writeImageModels(&out, report, false, "openai", nil, root))
	require.Empty(t, recorder, "suppressed hints must not register commands")
}

func TestSuggestedSavingErrorRegistersOnlyTypedRecovery(t *testing.T) {
	root := &cli.Command{Name: "openai"}
	var recorder commandRecorder
	commandsuggest.SetRecorder(root, &recorder)
	failure := imageSavingFailure("Try openai models list", errors.New("openai files delete --file-id untrusted"), "openai models list").(*SavingError)
	require.Equal(t, "Try openai models list", failure.Error())
	require.Empty(t, recorder)
	var out bytes.Buffer
	presenter := clihelp.NewSuggestedText(&out, root)
	require.NoError(t, presenter.WriteText(failure.ReadableMessage(root, presenter)))
	require.Equal(t, commandRecorder{{&out, "", "openai models list"}}, recorder)
	require.NotContains(t, out.String(), "untrusted")
}
