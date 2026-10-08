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
	"unicode/utf8"

	"charm.land/bubbles/v2/spinner"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestImageLoadingPromptIsQuotedEscapedAndBounded(t *testing.T) {
	prompt := "A 'cat'\\tree\n\t\x1b]52;c;synthetic\a\u202esecret"
	safe := loadingPromptText(prompt)
	label := loadingPromptLabel("Generating image", safe, 100)
	require.True(t, strings.HasPrefix(label, "Generating image '"))
	require.True(t, strings.HasSuffix(label, "'"))
	require.Contains(t, label, `\'cat\'`)
	require.Contains(t, label, `\\tree\n\t`)
	require.Contains(t, label, `\u202e`)
	require.NotContains(t, label, "\x1b")
	require.NotContains(t, label, "\a")
	require.NotContains(t, label, "\n")
	require.NotContains(t, label, "\t")
	require.NotContains(t, label, "\u202e")

	safe = loadingPromptText(strings.Repeat("🐈森", 1<<18))
	require.Less(t, len(safe), 520, "large prompts only create a bounded display copy")
	require.True(t, utf8.ValidString(safe))
	for _, columns := range []int{25, 40, 80} {
		label := loadingPromptLabel("Generating image", safe, columns)
		require.Less(t, ansi.StringWidth(label), columns)
		require.True(t, strings.HasSuffix(label, "...'"))
	}
	require.Equal(t, "Generating image", loadingPromptLabel("Generating image", safe, 20))
}

func TestImageLoadingStatusKeepsElapsedAndSavingWithoutEstimates(t *testing.T) {
	for stage := imageLoadingWaiting; stage <= imageLoadingSaved; stage++ {
		for _, columns := range []int{10, 20, 35, 80, 100} {
			line := loadingStatusLine(stage, 65*time.Second, columns)
			require.Less(t, ansi.StringWidth(line), columns)
			for _, removed := range []string{"Estimated", "%", "[", "]", "━", "#"} {
				require.NotContains(t, line, removed)
			}
			if stage == imageLoadingSaved && columns >= 20 {
				require.Contains(t, line, "Images saved")
				require.NotContains(t, line, "Ctrl+C")
			}
			if columns >= 20 {
				require.Contains(t, line, "1m 05s")
			}
		}
	}
	require.Contains(t, loadingStatusLine(imageLoadingWaiting, -time.Second, 80), "0s elapsed")
	for _, elapsed := range []time.Duration{9 * time.Second, 10 * time.Second, 59 * time.Second, time.Minute, time.Hour, time.Duration(1<<63 - 1)} {
		for columns := 20; columns <= 100; columns++ {
			require.Less(t, ansi.StringWidth(loadingStatusLine(imageLoadingWaiting, elapsed, columns)), columns)
		}
	}
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

func TestLoadingFeedbackElapsedAndConfirmedSavingStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var output lockedLoadingBuffer
		stop, advance := startLoadingFeedback(t.Context(), &output, "Generating image", "synthetic prompt", true, spinner.Line, func() (int, int) { return 100, 24 })
		time.Sleep(imageLoadingDelay)
		synctest.Wait()
		require.Contains(t, output.String(), "0s elapsed | Ctrl+C to cancel")
		time.Sleep(65 * time.Second)
		synctest.Wait()
		require.Contains(t, output.String(), "1m 05s elapsed | Ctrl+C to cancel")
		require.NotContains(t, output.String(), "Estimated")
		require.NotContains(t, output.String(), "#")
		require.NotContains(t, output.String(), "Images saved")
		advance(imageLoadingSaving)
		synctest.Wait()
		require.Contains(t, output.String(), "1m 05s elapsed")
		require.Contains(t, output.String(), "Saving image 'synthetic prompt'")
		advance(imageLoadingWaiting)
		synctest.Wait()
		require.Contains(t, output.String()[strings.LastIndex(output.String(), "\r\x1b[J"):], "Saving image")
		advance(imageLoadingSaved)
		stop()
		require.True(t, strings.HasSuffix(output.String(), "Images saved | 1m 05s elapsed\n"))
		require.NotContains(t, output.String()[strings.LastIndex(output.String(), "\r\x1b[J"):], "synthetic prompt")
	})
}

func TestLoadingFeedbackStoppedOrCanceledCannotCompleteLater(t *testing.T) {
	for _, cancelFirst := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var output bytes.Buffer
			stop, advance := startLoadingFeedback(ctx, &output, "Generating image", "", true, spinner.Line, func() (int, int) { return 100, 24 })
			time.Sleep(imageLoadingDelay)
			synctest.Wait()
			if cancelFirst {
				cancel()
			}
			stop()
			before := output.String()
			var group sync.WaitGroup
			for range 8 {
				group.Go(func() { advance(imageLoadingSaving); advance(imageLoadingSaved); stop() })
			}
			group.Wait()
			synctest.Wait()
			require.Equal(t, before, output.String())
			require.NotContains(t, output.String(), "Images saved")
		})
	}
}

func TestLoadingFeedbackDelayAndStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var output bytes.Buffer
		stop, advance := startLoadingFeedback(t.Context(), &output, "Generating image", "", true, spinner.Line, func() (int, int) { return 80, 24 })
		time.Sleep(imageLoadingDelay - time.Nanosecond)
		synctest.Wait()
		require.Empty(t, output.String())
		advance(imageLoadingSaving)
		advance(imageLoadingSaved)
		stop()
		stop()
		time.Sleep(time.Second)
		synctest.Wait()
		require.Empty(t, output.String())
	})
}

func TestLoadingFeedbackConcurrentStagesNeverRegress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var output bytes.Buffer
		stop, advance := startLoadingFeedback(t.Context(), &output, "Generating image", "", true, spinner.Line, func() (int, int) { return 100, 24 })
		time.Sleep(imageLoadingDelay)
		synctest.Wait()
		var group sync.WaitGroup
		for range 8 {
			group.Go(func() {
				advance(imageLoadingSaving)
				advance(imageLoadingSaved)
				advance(imageLoadingWaiting)
			})
		}
		group.Wait()
		synctest.Wait()
		stop()
		final := output.String()[strings.LastIndex(output.String(), "\r\x1b[J"):]
		require.Contains(t, final, "Images saved")
		require.NotContains(t, final, "Estimated")
		require.Equal(t, 2, strings.Count(output.String(), "\n"), "reserve once and print one completion line")
	})
}

func TestLoadingFeedbackStopsAndClearsBeforeReturning(t *testing.T) {
	for _, cancelContext := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var output bytes.Buffer
			stop, _ := startLoadingFeedback(ctx, &output, "Generating image", "", true, spinner.Line, func() (int, int) { return 80, 24 })
			time.Sleep(imageLoadingDelay + 250*time.Millisecond)
			synctest.Wait()
			require.Contains(t, output.String(), "\x1b[2K| Generating image")
			require.Contains(t, output.String(), "\x1b[2K/ Generating image")
			require.True(t, strings.HasSuffix(output.String(), "Generating image"), "leave the visible cursor after the label, clear of the spinner")
			if cancelContext {
				cancel()
			}
			var wait sync.WaitGroup
			for range 4 {
				wait.Go(stop)
			}
			wait.Wait()
			saved := output.String()
			require.True(t, strings.HasSuffix(saved, "\r\x1b[J"))
			require.Equal(t, 1, strings.Count(saved, "\n"), "reserve the second row only once")
			require.NotContains(t, saved, "\x1b[?7", "preserve terminal autowrap mode")
			require.NotContains(t, saved, "\x1b[?25")
			time.Sleep(time.Second)
			synctest.Wait()
			require.Equal(t, saved, output.String(), "stop joins the worker; it cannot redraw later")
		})
	}
}

func TestLoadingFeedbackShowsElapsedTimeWithoutEstimates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var output bytes.Buffer
		stop, _ := startLoadingFeedback(t.Context(), &output, "Generating image", "", true, spinner.Line, func() (int, int) { return 80, 24 })
		time.Sleep(imageLoadingDelay + 65*time.Second)
		synctest.Wait()
		stop()
		require.Contains(t, output.String(), "0s elapsed | Ctrl+C to cancel")
		require.Contains(t, output.String(), "2s elapsed | Ctrl+C to cancel")
		require.Contains(t, output.String(), "1m 05s elapsed | Ctrl+C to cancel")
		require.NotContains(t, output.String(), "%", "elapsed time must not imply a completion percentage")
		require.Equal(t, 1, strings.Count(output.String(), "\n"), "redraw without adding permanent lines")
	})
}

func TestLoadingFeedbackKeepsElapsedTimeOnNarrowTerminals(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var output bytes.Buffer
		stop, _ := startLoadingFeedback(t.Context(), &output, "Generating image", "", true, spinner.Line, func() (int, int) { return 20, 24 })
		time.Sleep(imageLoadingDelay + time.Second)
		synctest.Wait()
		stop()
		require.Contains(t, output.String(), "1s elapsed")
		require.NotContains(t, output.String(), "Ctrl+C")
		require.Equal(t, 1, strings.Count(output.String(), "\n"))
	})
}

func TestLoadingFeedbackResizeClearsShorterStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var output bytes.Buffer
		var width atomic.Int64
		width.Store(80)
		stop, _ := startLoadingFeedback(t.Context(), &output, "Generating image", "", true, spinner.Line, func() (int, int) { return int(width.Load()), 24 })
		time.Sleep(imageLoadingDelay)
		synctest.Wait()
		width.Store(20)
		time.Sleep(spinner.Line.FPS)
		synctest.Wait()
		width.Store(80)
		time.Sleep(spinner.Line.FPS)
		synctest.Wait()
		stop()
		cleared := strings.Split(output.String(), "\r\x1b[J")
		require.Len(t, cleared, 5, "clear before each of three frames and after stopping")
		require.Contains(t, cleared[1], "Ctrl+C to cancel")
		require.Contains(t, cleared[2], "0s elapsed")
		require.NotContains(t, cleared[2], "Ctrl+C")
		require.Contains(t, cleared[3], "Ctrl+C to cancel", "restore the hint after the terminal grows")
	})
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
				stop, _ := startLoadingFeedback(t.Context(), &output, "Generating image", "", test.animate, spinner.Line, func() (int, int) { return test.width, 24 })
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
		stop, _ := startLoadingFeedback(t.Context(), &output, "Generating image", "", true, spinner.Line, func() (int, int) { return int(width.Load()), 24 })
		time.Sleep(imageLoadingDelay)
		synctest.Wait()
		width.Store(12)
		time.Sleep(time.Second)
		synctest.Wait()
		width.Store(80)
		time.Sleep(time.Second)
		synctest.Wait()
		stop()
		require.Contains(t, output.String(), "Ctrl+C to cancel")
		require.True(t, strings.HasSuffix(output.String(), "\r\x1b[JWorking...\n"))
		require.Equal(t, 1, strings.Count(output.String(), "Generating image"), "do not resume after static fallback")
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
			stop, _ := startLoadingFeedback(t.Context(), writer, "Generating image", "", true, spinner.Line, func() (int, int) { return 80, 24 })
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

func TestLoadingFeedbackPartialFrameClearsBothRows(t *testing.T) {
	for _, cutoff := range []string{"\x1b[J", "\x1b7", "\x1b[80G", "elapsed", "\x1b8", "⣾"} {
		t.Run(cutoff, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var output bytes.Buffer
				calls, partial := 0, 0
				writer := loadingTestWriter(func(p []byte) (int, error) {
					calls++
					if calls == 2 {
						// Stop inside a terminal instruction, lower row, or UTF-8 frame.
						partial = strings.Index(string(p), cutoff) + 1
						require.Positive(t, partial)
						output.Write(p[:partial])
						return partial, io.ErrShortWrite
					}
					return output.Write(p)
				})
				animation := imageLoadingSpinner(func(key string) string {
					return map[string]string{"TERM": "xterm-256color", "LANG": "en_US.UTF-8"}[key]
				}, "darwin")
				stop, _ := startLoadingFeedback(t.Context(), writer, "Generating image", "synthetic", true, animation, func() (int, int) { return 80, 24 })
				time.Sleep(imageLoadingDelay + time.Second)
				synctest.Wait()
				stop()
				require.Equal(t, 3, calls, "stop drawing after failure, then attempt one cleanup")
				cleanup := "\x18\r\x1b[J"
				if partial >= len("\r\x1b[J\x1b7") {
					cleanup = "\x18\x1b8\r\x1b[J"
				}
				require.True(t, strings.HasSuffix(output.String(), cleanup))
			})
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
			stop, _ := startLoadingFeedback(t.Context(), &output, "Generating image", "", true, animation, func() (int, int) { return columns, 24 })
			time.Sleep(imageLoadingDelay + 2*animation.FPS)
			synctest.Wait()
			stop()
			if columns == 19 {
				require.Equal(t, "Generating image\n", output.String(), "leave one column to prevent wrapping")
			} else {
				require.Contains(t, output.String(), "\x1b[2K⣾  Generating image")
				require.Contains(t, output.String(), "\x1b[2K⣽  Generating image")
				require.True(t, strings.HasSuffix(output.String(), "\r\x1b[J"))
				require.Equal(t, 1, strings.Count(output.String(), "\n"))
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

func TestLoadingFeedbackFailedClearDoesNotPrintFallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var output bytes.Buffer
		var width atomic.Int64
		width.Store(80)
		failed := false
		writer := loadingTestWriter(func(p []byte) (int, error) {
			if !failed && string(p) == "\r\x1b[J" {
				failed = true
				return 0, nil
			}
			return output.Write(p)
		})
		stop, _ := startLoadingFeedback(t.Context(), writer, "Generating image", "", true, spinner.Line, func() (int, int) { return int(width.Load()), 24 })
		time.Sleep(imageLoadingDelay)
		synctest.Wait()
		width.Store(12)
		time.Sleep(time.Second)
		synctest.Wait()
		stop()
		require.True(t, failed)
		require.NotContains(t, output.String(), "Working...", "do not append output after a short cleanup write")
		require.True(t, strings.HasSuffix(output.String(), "\r\x1b[J"))
	})
}

func TestLoadingFeedbackShortTerminalUsesStaticPrivateFallback(t *testing.T) {
	for _, height := range []int{0, 1} {
		synctest.Test(t, func(t *testing.T) {
			var output bytes.Buffer
			stop, _ := startLoadingFeedback(t.Context(), &output, "Generating image", "synthetic private prompt", true, spinner.Line, func() (int, int) { return 80, height })
			time.Sleep(imageLoadingDelay + time.Second)
			synctest.Wait()
			stop()
			require.Equal(t, "Generating image\n", output.String())
		})
	}
}
