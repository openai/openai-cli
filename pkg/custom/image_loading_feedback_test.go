package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestLoadingFeedbackDelayAndStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var output bytes.Buffer
		stop := startLoadingFeedback(t.Context(), &output, "Generating image...", true, func() int { return 80 })
		time.Sleep(imageLoadingDelay - time.Nanosecond)
		synctest.Wait()
		require.Empty(t, output.String())
		stop()
		stop()
		time.Sleep(time.Second)
		synctest.Wait()
		require.Empty(t, output.String())
	})
}

func TestLoadingFeedbackStopsAndClearsBeforeReturning(t *testing.T) {
	for _, cancelContext := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var output bytes.Buffer
			stop := startLoadingFeedback(ctx, &output, "Generating image...", true, func() int { return 80 })
			time.Sleep(imageLoadingDelay + 250*time.Millisecond)
			synctest.Wait()
			require.Contains(t, output.String(), "\r| Generating image...")
			require.Contains(t, output.String(), "\r/ Generating image...")
			require.NotContains(t, output.String(), "\x1b[2K")
			if cancelContext {
				cancel()
			}
			var wait sync.WaitGroup
			for range 4 {
				wait.Go(stop)
			}
			wait.Wait()
			saved := output.String()
			require.True(t, strings.HasSuffix(saved, "\r\x1b[2K"))
			require.Equal(t, 1, strings.Count(saved, "\x1b[2K"))
			require.NotContains(t, saved, "\x1b[?25")
			time.Sleep(time.Second)
			synctest.Wait()
			require.Equal(t, saved, output.String(), "stop joins the worker; it cannot redraw later")
		})
	}
}

func TestLoadingFeedbackStaticAndNarrowTerminals(t *testing.T) {
	for _, test := range []struct {
		name    string
		animate bool
		width   int
		want    string
	}{
		{"unsupported", false, 80, "Generating image...\n"},
		{"unknown size", true, 0, "Generating image...\n"},
		{"barely fits", true, 20, "Generating image...\n"},
		{"narrow", true, 19, "Working...\n"},
		{"tiny", true, 10, "...\n"},
		{"no room", true, 3, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var output bytes.Buffer
				stop := startLoadingFeedback(t.Context(), &output, "Generating image...", test.animate, func() int { return test.width })
				time.Sleep(imageLoadingDelay + time.Second)
				synctest.Wait()
				stop()
				require.Equal(t, test.want, output.String())
			})
		})
	}
}

func TestLoadingFeedbackResizeStopsAnimation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var output bytes.Buffer
		var width atomic.Int64
		width.Store(80)
		stop := startLoadingFeedback(t.Context(), &output, "Generating image...", true, func() int { return int(width.Load()) })
		time.Sleep(imageLoadingDelay)
		synctest.Wait()
		width.Store(12)
		time.Sleep(time.Second)
		synctest.Wait()
		width.Store(80)
		time.Sleep(time.Second)
		synctest.Wait()
		stop()
		require.Equal(t, "\r| Generating image...\r\x1b[2KWorking...\n", output.String())
	})
}

func TestLoadingFeedbackWriteFailureStopsRetries(t *testing.T) {
	for _, short := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			calls := 0
			writer := loadingTestWriter(func(p []byte) (int, error) {
				calls++
				if short {
					return 1, nil
				}
				return 0, io.ErrClosedPipe
			})
			stop := startLoadingFeedback(t.Context(), writer, "Generating image...", true, func() int { return 80 })
			time.Sleep(imageLoadingDelay + time.Second)
			synctest.Wait()
			stop()
			wantCalls := 1
			if short {
				wantCalls++ // Best-effort cleanup for a partially drawn frame.
			}
			require.Equal(t, wantCalls, calls)
		})
	}
}

func TestLoadingAnimationEnvironment(t *testing.T) {
	for _, test := range []struct {
		term, ci string
		want     bool
	}{
		{"xterm-256color", "", true}, {"xterm-256color", "false", true},
		{"xterm-256color", "0", true}, {"xterm-256color", "true", false},
		{"xterm-256color", "1", false}, {"dumb", "", false},
		{"", "", false}, {"unknown", "", false}, {"screen-256color", "", true},
		{"tmux-256color", "", true}, {"xterm-kitty", "", true},
	} {
		getenv := func(key string) string {
			if key == "TERM" {
				return test.term
			}
			if key == "CI" {
				return test.ci
			}
			return ""
		}
		require.Equal(t, test.want, loadingAnimationSupported(getenv), "TERM=%q CI=%q", test.term, test.ci)
	}
}

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
		})
		require.True(t, called)
		require.Same(t, sentinel, err)
		if plan != nil {
			require.Nil(t, plan.stopLoading)
		}
	}
}

func TestImageLoadingInterruptRetainsCause(t *testing.T) {
	cause := imageSavingFailure("Check saved files before trying again.", context.Canceled)
	err := &imageLoadingInterrupt{cause}
	require.ErrorIs(t, err, context.Canceled)
	var exit cli.ExitCoder
	require.ErrorAs(t, err, &exit)
	require.Equal(t, 130, exit.ExitCode())
	var saving *imageSavingError
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
