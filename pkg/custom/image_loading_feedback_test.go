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

	"charm.land/bubbles/v2/spinner"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestLoadingFeedbackDelayAndStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var output bytes.Buffer
		stop := startLoadingFeedback(t.Context(), &output, "Generating image", true, spinner.Line, func() int { return 80 })
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
			stop := startLoadingFeedback(ctx, &output, "Generating image", true, spinner.Line, func() int { return 80 })
			time.Sleep(imageLoadingDelay + 250*time.Millisecond)
			synctest.Wait()
			require.Contains(t, output.String(), "\r| Generating image")
			require.Contains(t, output.String(), "\r/ Generating image")
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
		{"unsupported", false, 80, "Generating image\n"},
		{"unknown size", true, 0, "Generating image\n"},
		{"barely fits", true, 17, "Generating image\n"},
		{"narrow", true, 16, "Working...\n"},
		{"tiny", true, 10, "...\n"},
		{"no room", true, 3, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var output bytes.Buffer
				stop := startLoadingFeedback(t.Context(), &output, "Generating image", test.animate, spinner.Line, func() int { return test.width })
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
		stop := startLoadingFeedback(t.Context(), &output, "Generating image", true, spinner.Line, func() int { return int(width.Load()) })
		time.Sleep(imageLoadingDelay)
		synctest.Wait()
		width.Store(12)
		time.Sleep(time.Second)
		synctest.Wait()
		width.Store(80)
		time.Sleep(time.Second)
		synctest.Wait()
		stop()
		require.Equal(t, "\r| Generating image\r\x1b[2KWorking...\n", output.String())
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
			stop := startLoadingFeedback(t.Context(), writer, "Generating image", true, spinner.Line, func() int { return 80 })
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

func TestLoadingFeedbackPartialFrameClearsLine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var output bytes.Buffer
		calls := 0
		writer := loadingTestWriter(func(p []byte) (int, error) {
			calls++
			if calls == 1 {
				// The terminal received only part of the UTF-8 frame.
				output.Write(p[:2])
				return 2, io.ErrShortWrite
			}
			return output.Write(p)
		})
		animation := imageLoadingSpinner(func(key string) string {
			return map[string]string{"TERM": "xterm-256color", "LANG": "en_US.UTF-8"}[key]
		}, "darwin")
		stop := startLoadingFeedback(t.Context(), writer, "Generating image", true, animation, func() int { return 80 })
		time.Sleep(imageLoadingDelay + time.Second)
		synctest.Wait()
		stop()
		require.Equal(t, 2, calls, "stop retrying frames, but attempt to restore the terminal")
		require.Equal(t, "\r\xe2\r\x1b[2K", output.String())
	})
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

func TestImageLoadingSpinnerEnvironment(t *testing.T) {
	for _, test := range []struct {
		name, goos string
		env        map[string]string
		unicode    bool
	}{
		{"UTF-8", "darwin", map[string]string{"LANG": "en_US.UTF-8"}, true},
		{"UTF8", "linux", map[string]string{"LANG": "C.utf8"}, true},
		{"unset locale", "linux", nil, false},
		{"C locale", "linux", map[string]string{"LANG": "C"}, false},
		{"LC_ALL overrides LANG", "darwin", map[string]string{"LANG": "en_US.UTF-8", "LC_ALL": "C"}, false},
		{"LC_CTYPE overrides LANG", "darwin", map[string]string{"LANG": "C", "LC_CTYPE": "UTF-8"}, true},
		{"LC_ALL overrides LC_CTYPE", "darwin", map[string]string{"LC_ALL": "C", "LC_CTYPE": "UTF-8"}, false},
		{"legacy Windows", "windows", map[string]string{"LANG": "en_US.UTF-8"}, false},
		{"Windows Terminal", "windows", map[string]string{"WT_SESSION": "synthetic"}, true},
		{"NO_COLOR any value", "darwin", map[string]string{"LANG": "en_US.UTF-8", "NO_COLOR": "0"}, true},
		{"NO_COLOR beats force", "darwin", map[string]string{"LANG": "en_US.UTF-8", "NO_COLOR": "1", "FORCE_COLOR": "1"}, true},
		{"CLICOLOR off", "darwin", map[string]string{"LANG": "en_US.UTF-8", "CLICOLOR": "0"}, true},
		{"FORCE_COLOR off", "darwin", map[string]string{"LANG": "en_US.UTF-8", "FORCE_COLOR": "0"}, true},
		{"dumb", "darwin", map[string]string{"TERM": "dumb"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			getenv := func(key string) string {
				if value, ok := test.env[key]; ok {
					return value
				}
				if key == "TERM" {
					return "xterm-256color"
				}
				return ""
			}
			animation := imageLoadingSpinner(getenv, test.goos)
			want := spinner.Line
			if test.unicode {
				want = spinner.Dot
			}
			require.Equal(t, want.FPS, animation.FPS)
			require.Len(t, animation.Frames, len(want.Frames))
			for i, frame := range animation.Frames {
				require.Equal(t, want.Frames[i], frame, "keep the terminal foreground without styling escapes")
				require.Equal(t, ansi.StringWidth(want.Frames[i]), ansi.StringWidth(frame))
				require.NotContains(t, frame, "\x1b[")
			}
		})
	}
	require.Equal(t, "⣾ ", spinner.Dot.Frames[0], "shared library frames must stay unmodified")
	require.Equal(t, "|", spinner.Line.Frames[0])
}

func TestLoadingFeedbackUnicodeFitsByCells(t *testing.T) {
	for _, columns := range []int{19, 20} {
		synctest.Test(t, func(t *testing.T) {
			var output bytes.Buffer
			animation := imageLoadingSpinner(func(key string) string {
				return map[string]string{"TERM": "xterm-256color", "LANG": "en_US.UTF-8"}[key]
			}, "darwin")
			stop := startLoadingFeedback(t.Context(), &output, "Generating image", true, animation, func() int { return columns })
			time.Sleep(imageLoadingDelay + 2*animation.FPS)
			synctest.Wait()
			stop()
			if columns == 19 {
				require.Equal(t, "Generating image\n", output.String(), "leave one column to prevent wrapping")
			} else {
				require.Contains(t, output.String(), "\r⣾  Generating image")
				require.Contains(t, output.String(), "\r⣽  Generating image")
				require.True(t, strings.HasSuffix(output.String(), "\r\x1b[2K"))
				require.NotContains(t, output.String(), "\n")
			}
		})
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
