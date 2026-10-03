package custom

import (
	"context"
	"errors"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestImagePickerSelectionsRestoreRequestFlagState(t *testing.T) {
	app := &cli.Command{Name: "generate", Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "prompt", BodyPath: "prompt"},
		&requestflag.Flag[*string]{Name: "model", BodyPath: "model"},
		&requestflag.Flag[*int64]{Name: "n", Aliases: []string{"count"}, BodyPath: "n", Default: requestflag.Ptr[int64](1)},
	}}
	app.Action = func(ctx context.Context, command *cli.Command) error {
		before := requestflag.ExtractRequestContents(command)
		for range 2 {
			func() {
				for _, pair := range [][2]string{{"prompt", "a blue cat"}, {"model", "gpt-image-2"}, {"count", "2"}} {
					restore, err := setImagePickerFlag(command, pair[0], pair[1])
					require.NoError(t, err)
					defer restore()
				}
				require.Equal(t, map[string]any{"prompt": "a blue cat", "model": requestflag.Ptr("gpt-image-2"), "n": requestflag.Ptr[int64](2)}, requestflag.ExtractRequestContents(command).Body)
			}()
			require.Equal(t, before, requestflag.ExtractRequestContents(command), "a later action must not inherit picker selections")
			require.False(t, command.IsSet("model"))
			require.False(t, command.IsSet("n"))
			require.True(t, command.IsSet("prompt"))
			require.Equal(t, "original", command.String("prompt"))
		}
		_, err := setImagePickerFlag(command, "count", "invalid")
		require.Error(t, err)
		require.Equal(t, before, requestflag.ExtractRequestContents(command))
		_, err = setImagePickerFlag(command, "missing", "value")
		require.Error(t, err)
		return nil
	}
	require.NoError(t, app.Run(context.Background(), []string{"openai", "--prompt", "original"}))
}

func TestImagePickerCanceledContextDoesNotOpenTerminal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := runImagePicker(ctx, nil, nil, imagePickerOptions{})
	require.True(t, errors.Is(err, context.Canceled))
	require.Empty(t, result.Args)
}
