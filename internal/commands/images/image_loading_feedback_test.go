package images

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"charm.land/bubbles/v2/spinner"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestImageLoadingPromptUsesOnlyCurrentPickerDisplay(t *testing.T) {
	const privatePrompt = "SYNTHETIC_INDIRECT_REQUEST_CONTENT"
	const visiblePrompt = "Synthetic prompt shown in the picker"
	app := &cli.Command{Name: "generate"}
	registerImageSavingFlags(app)
	app.Action = func(ctx context.Context, command *cli.Command) error {
		body := gjson.Parse(`{"prompt":"` + privatePrompt + `","model":"gpt-image-2"}`)
		pickerCtx := context.WithValue(ctx, imagePickerLoadingPromptKey{}, visiblePrompt)
		for _, test := range []struct {
			name string
			ctx  context.Context
			want string
		}{
			{"direct request", ctx, ""},
			{"picker request", pickerCtx, visiblePrompt},
			{"reused parent", ctx, ""},
		} {
			t.Run(test.name, func(t *testing.T) {
				plan, _, err := prepareImageSaving(test.ctx, command, body)
				require.NoError(t, err)
				require.NotNil(t, plan)
				require.Equal(t, privatePrompt, body.Get("prompt").String(), "display selection must not modify request data")
				synctest.Test(t, func(t *testing.T) {
					var output bytes.Buffer
					stop, _ := clioutput.StartLoadingFeedback(t.Context(), &output, "Generating image", plan.loadingPrompt, true, spinner.Line, func() (int, int) { return 100, 24 })
					time.Sleep(clioutput.LoadingDelay + time.Second)
					stop()
					require.NotContains(t, output.String(), privatePrompt)
					require.Contains(t, output.String(), "Generating image")
					require.Contains(t, output.String(), "1s elapsed")
					if test.want == "" {
						require.NotContains(t, output.String(), visiblePrompt)
						require.NotContains(t, output.String(), "Generating image '")
					} else {
						require.Contains(t, output.String(), "Generating image '"+test.want+"'")
					}
				})
			})
		}
		return nil
	}
	require.NoError(t, app.Run(t.Context(), []string{"openai", "--output-dir", t.TempDir(), "--name", "synthetic-safe-name"}))
}

type lockedLoadingBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedLoadingBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *lockedLoadingBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func TestImageLoadingInterruptRetainsCause(t *testing.T) {
	cause := imageSavingFailure("Check saved files before trying again.", context.Canceled)
	err := &imageLoadingInterrupt{cause}
	require.ErrorIs(t, err, context.Canceled)
	var exit cli.ExitCoder
	require.ErrorAs(t, err, &exit)
	require.Equal(t, 130, exit.ExitCode())
	var saving *SavingError
	require.ErrorAs(t, err, &saving)
	require.Equal(t, cause.Error(), err.Error())
}

func TestImageLoadingStopsBeforeVisibleOutput(t *testing.T) {
	for _, progress := range []bool{false, true} {
		stopped, wrote := false, false
		plan := &imageOutputPlan{
			directory: t.TempDir(), name: "synthetic", inline: "off",
			stopLoading: func() { stopped = true },
		}
		writer := loadingTestWriter(func(p []byte) (int, error) {
			require.True(t, stopped, "feedback must stop before any image label, warning or saved path")
			wrote = true
			return len(p), nil
		})
		if progress {
			_, err := plan.displayImageProgress(t.Context(), gjson.Parse(`{}`), writer, "kitty")
			require.NoError(t, err)
		} else {
			require.NoError(t, plan.save(t.Context(), []byte(`{"data":[{"b64_json":"iVBORw0KGgo="}]}`), writer))
		}
		require.True(t, wrote)
	}
}

type loadingTestWriter func([]byte) (int, error)

func (w loadingTestWriter) Write(p []byte) (int, error) { return w(p) }

func TestImageLoadingLeavesNoninteractiveActionUnchanged(t *testing.T) {
	for _, plan := range []*imageOutputPlan{nil, {}} {
		command := &cli.Command{Name: "generate", Writer: io.Discard}
		ctx := t.Context()
		sentinel := errors.New("original action failure")
		called := false
		err := runWithImageLoading(ctx, command, plan, func(got context.Context, gotCommand *cli.Command) error {
			called = true
			require.Same(t, ctx, got)
			require.Same(t, command, gotCommand)
			return sentinel
		}, SavingOptions{})
		require.True(t, called)
		require.Same(t, sentinel, err)
		if plan != nil {
			require.Nil(t, plan.stopLoading)
		}
	}
}
