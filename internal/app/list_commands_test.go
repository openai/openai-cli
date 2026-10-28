package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-cli/internal/output/listview"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestResourceListInterruptPreservesDiagnosticAndCauses(t *testing.T) {
	apiFailure := &openai.Error{StatusCode: 400, Message: "synthetic list failure"}
	for _, code := range []int{130, 143} {
		for _, cause := range []error{context.Canceled, errors.Join(context.Canceled, apiFailure), errors.Join(context.Canceled, io.ErrClosedPipe)} {
			failure := &listCommandInterrupt{cause: cause, code: code}
			var exit cli.ExitCoder
			require.True(t, errors.As(failure, &exit))
			require.Equal(t, code, exit.ExitCode())
			require.ErrorIs(t, failure, cause)
			for _, format := range []string{"text", "json"} {
				var out bytes.Buffer
				require.NoError(t, ShowCommandError(readableErrorTestCommand(t, "--format-error", format), failure, &out))
				require.NotEmpty(t, out.String(), "a signal must not suppress error output")
				if format == "json" && errors.Is(cause, apiFailure) {
					require.Contains(t, out.String(), `"status_code": 400`)
				} else {
					require.Contains(t, out.String(), "Request canceled.")
				}
			}
		}
	}
}

func TestResourceListViewerQuitAndInterrupt(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{{Code: 'c', Mod: tea.ModCtrl}, {Code: 'q', Text: "q"}, {Code: tea.KeyEscape}} {
		for _, outputErr := range []error{nil, io.ErrClosedPipe} {
			ctx, cancel := context.WithCancel(t.Context())
			model := listview.New(listViewerOptions(ShowJSONOpts{
				Context: t.Context(), Operation: "(resource) files > (method) list", CallerHandlesSignals: true,
			}), nil, cancel, 80, 24)
			_, quit := model.Update(key)
			require.NotNil(t, quit)
			require.ErrorIs(t, ctx.Err(), context.Canceled)
			failure := model.Finish(outputErr, tea.ErrProgramKilled)
			if outputErr != nil {
				require.ErrorIs(t, failure, outputErr)
			}
			var exit cli.ExitCoder
			if key.String() == "ctrl+c" && outputErr == nil {
				require.True(t, errors.As(failure, &exit))
				require.Equal(t, 130, exit.ExitCode())
			} else {
				require.False(t, errors.As(failure, &exit))
				if outputErr == nil {
					require.NoError(t, failure)
				}
			}
		}
	}
}
