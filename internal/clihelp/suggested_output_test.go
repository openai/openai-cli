package clihelp

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/commandsuggest"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

type recordedCommand struct {
	output      io.Writer
	shell, text string
}
type commandRecorder []recordedCommand

func (r *commandRecorder) Record(output io.Writer, shell, text string) {
	*r = append(*r, recordedCommand{output, shell, text})
}

type commandLinkRecorder struct{ commandRecorder }

func (r *commandLinkRecorder) Render(output io.Writer, shell, text string) string {
	r.Record(output, shell, text)
	return "\x1b]8;;openai-insert://Ab01234567/0123456789abcdef0123456789abcdef/1\x1b\\" + text + "\x1b]8;;\x1b\\"
}
func TestSuggestedTextEscapesExternalControlsBeforeLinks(t *testing.T) {
	root := &cli.Command{Name: "openai"}
	var recorder commandLinkRecorder
	commandsuggest.SetRecorder(root, &recorder)
	var output bytes.Buffer
	presenter := NewSuggestedText(&output, root)
	text := "External \x1b[2J\u202e. Try " + SuggestedCommand(root, []io.Writer{presenter}, "openai models list") + "."
	require.NoError(t, presenter.WriteText(text))
	require.NotContains(t, output.String(), "\x1b[2J")
	require.Equal(t, 2, strings.Count(output.String(), "\x1b]8;"))
	require.Contains(t, ansi.Strip(output.String()), "Try openai models list.")
	require.Equal(t, commandRecorder{{&output, "", "openai models list"}}, recorder.commandRecorder)
}
func TestSuggestedTextDoesNotConvertUnsafeSnippetsIntoLinks(t *testing.T) {
	root := &cli.Command{Name: "openai"}
	var recorder commandLinkRecorder
	commandsuggest.SetRecorder(root, &recorder)
	var output bytes.Buffer
	presenter := NewSuggestedText(&output, root)
	message := SuggestedCommand(root, []io.Writer{presenter}, "openai \x1b[2J")
	require.NoError(t, presenter.WriteText(message))
	require.Empty(t, recorder.commandRecorder)
	require.NotContains(t, output.String(), "\x1b")
}

func TestSuggestedTextWrappedWriterKeepsPhysicalDestination(t *testing.T) {
	root := &cli.Command{Name: "openai"}
	var output bytes.Buffer
	var recorder commandRecorder
	commandsuggest.SetRecorder(root, &recorder)
	presenter := NewSuggestedTextTo(io.MultiWriter(&output), &output, root)
	command := SuggestedCommand(root, []io.Writer{presenter}, "openai help")
	require.NoError(t, presenter.WriteText(command))
	require.Equal(t, commandRecorder{{&output, "", "openai help"}}, recorder)
}

type invalidatableCommandRecorder struct {
	commandLinkRecorder
	invalidated bool
}

func (r *invalidatableCommandRecorder) Invalidate() {
	r.invalidated = true
	r.commandRecorder = nil
}

type suggestedWriteFailure struct {
	partial bool
	cause   error
}

func (w suggestedWriteFailure) Write(data []byte) (int, error) {
	if w.partial {
		return len(data) / 2, w.cause
	}
	return 0, w.cause
}

func TestSuggestedTextInvalidatesCaptureAfterFailedWrite(t *testing.T) {
	for _, test := range []struct {
		name   string
		writer io.Writer
		want   error
	}{
		{"failed", suggestedWriteFailure{cause: io.ErrClosedPipe}, io.ErrClosedPipe},
		{"partial failure", suggestedWriteFailure{partial: true, cause: io.ErrClosedPipe}, io.ErrClosedPipe},
		{"short write", suggestedWriteFailure{partial: true}, io.ErrShortWrite},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := &cli.Command{Name: "openai"}
			var recorder invalidatableCommandRecorder
			commandsuggest.SetRecorder(root, &recorder)
			var prior bytes.Buffer
			good := NewSuggestedText(&prior, root)
			require.NoError(t, good.WriteText(SuggestedCommand(root, []io.Writer{good}, "openai models list")))
			require.Len(t, recorder.commandRecorder, 1)
			presenter := NewSuggestedText(test.writer, root)
			text := "Next: " + SuggestedCommand(root, []io.Writer{presenter}, "openai help setup")
			err := presenter.WriteText(text)
			require.True(t, errors.Is(err, test.want), "write failure was lost: %v", err)
			require.True(t, recorder.invalidated, "a failed write left live insertion targets")
			require.Empty(t, recorder.commandRecorder, "previous and partial targets must both be invalidated")
		})
	}
}
