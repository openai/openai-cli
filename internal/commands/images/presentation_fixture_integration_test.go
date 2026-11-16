package images_test

import (
	"context"
	"io"
	"testing"

	"github.com/openai/openai-cli/internal/output/transform"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

const OutputResponse = transform.OutputResponse
const OutputStreamEvent = transform.OutputStreamEvent

type errorSink struct{ err error }

func (e errorSink) Write([]byte) (int, error) { return 0, e.err }
func readableErrorTestCommand(t *testing.T, args ...string) *cli.Command {
	t.Helper()
	root := &cli.Command{
		Name: "openai", Writer: io.Discard, ErrWriter: io.Discard, HideHelpCommand: true,
		Metadata: map[string]any{"help-invocation": "./openai"},
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Value: "auto"},
			&cli.StringFlag{Name: "format-error", Value: "auto"},
			&cli.StringFlag{Name: "transform-error"},
			&cli.StringFlag{Name: "transform"},
			&cli.BoolFlag{Name: "raw-output"},
		},
		Action: func(context.Context, *cli.Command) error { return nil },
	}
	require.NoError(t, root.Run(t.Context(), append([]string{"openai"}, args...)))
	return root
}

func assertReadableErrorContainsNoPrivateDetails(t *testing.T, text string) {
	t.Helper()
	for _, private := range []string{"synthetic-", "https://", "token=", "\x1b", "\a", "\u202e"} {
		require.NotContains(t, text, private)
	}
}

const imageGenerationOperation = "(resource) images > (method) generate"
