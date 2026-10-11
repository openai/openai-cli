package custom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/spinner"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/urfave/cli/v3"
)

const imageLoadingDelay = 400 * time.Millisecond

type imageLoadingStage int32

const (
	imageLoadingWaiting imageLoadingStage = iota
	imageLoadingSaving
	imageLoadingSaved
)

// Feedback starts after request preparation, so it never changes stdin reading
// or validation. It owns no request/output writers and cannot replace a failure.
func runWithImageLoading(ctx context.Context, command *cli.Command, plan *imageOutputPlan, next cli.ActionFunc) (err error) {
	root := command.Root()
	out := imageCommandWriter(command)
	if plan == nil || !isTerminal(out) ||
		root.Bool("debug") || errorOutputFormat(root) != "text" || root.String("transform-error") != "" {
		return next(ctx, command)
	}
	feedback := os.Stderr
	if plan.loadingPrompt != "" {
		// Keep the visible picker prompt on its UI surface, outside diagnostics.
		feedback = out.(*os.File) // isTerminal above requires an *os.File.
	}
	if !isTerminal(feedback) {
		return next(ctx, command)
	}
	label := "Generating image"
	switch command.Name {
	case "edit":
		label = "Editing image"
	case "create-variation":
		label = "Creating image variation"
	}
	parent := ctx
	ctx, stopSignals := signal.NotifyContext(ctx, os.Interrupt)
	// Restore normal signal handling once cancellation starts. A second Ctrl-C
	// can still terminate the process if a terminal write or cleanup is blocked.
	stopReset := context.AfterFunc(ctx, stopSignals)
	stopLoading := func() {}
	advance := func(imageLoadingStage) {}
	if outputDiagnosticsAllowed(ctx) {
		stopLoading, advance = startLoadingFeedback(ctx, feedback, label, plan.loadingPrompt, loadingAnimationSupported(os.Getenv), imageLoadingSpinner(os.Getenv, runtime.GOOS), func() (int, int) {
			width, height, _ := term.GetSize(feedback.Fd())
			return width, height
		})
	}
	plan.stopLoading = stopLoading
	plan.loadingStage = advance
	defer func() {
		stopLoading()
		stopReset()
		interrupted := ctx.Err() != nil && parent.Err() == nil
		stopSignals()
		var exit cli.ExitCoder
		if interrupted && (err == nil || errors.Is(err, context.Canceled)) && !errors.As(err, &exit) {
			if err == nil {
				err = ctx.Err()
			}
			err = &imageLoadingInterrupt{err}
		}
	}()
	return next(ctx, command)
}

// Keep the cancellation cause and any saving guidance while using the shell's
// conventional Ctrl-C status. Ordinary API/local errors keep their own status.
type imageLoadingInterrupt struct{ error }

func (e *imageLoadingInterrupt) Unwrap() error { return e.error }
func (e *imageLoadingInterrupt) ExitCode() int { return 130 }

func (p *imageOutputPlan) stopLoadingFeedback() {
	if p.stopLoading != nil {
		p.stopLoading()
	}
}

func (p *imageOutputPlan) setLoadingStage(stage imageLoadingStage) {
	if p != nil && p.loadingStage != nil {
		p.loadingStage(stage)
	}
}

func loadingAnimationSupported(getenv func(string) string) bool {
	ci := strings.ToLower(getenv("CI"))
	if ci != "" && ci != "0" && ci != "false" {
		return false
	}
	terminal := strings.ToLower(getenv("TERM"))
	for _, prefix := range []string{"xterm", "screen", "tmux", "vt100", "ansi", "rxvt", "linux", "alacritty", "foot", "wezterm", "ghostty", "st-"} {
		if strings.HasPrefix(terminal, prefix) {
			return true
		}
	}
	return false
}

func imageLoadingSpinner(getenv func(string) string, goos string) spinner.Spinner {
	animation := spinner.Line
	// Respect locale precedence: LC_ALL=C must not inherit UTF-8 from LANG.
	locale := getenv("LC_ALL")
	if locale == "" {
		locale = getenv("LC_CTYPE")
	}
	if locale == "" {
		locale = getenv("LANG")
	}
	utf8 := strings.Contains(strings.ToLower(strings.ReplaceAll(locale, "-", "")), "utf8")
	if (goos != "windows" && utf8) || (goos == "windows" && getenv("WT_SESSION") != "") {
		animation = spinner.Dot
	}
	// Use the terminal foreground so the indicator follows light and dark themes.
	return animation
}

// The picker supplies only prompt text already visible in its form. Clearing
// frames does not redact terminal captures. The caller stops this worker before
// another writer uses the terminal. Feedback writes are best effort; stop joins
// the worker without changing cursor visibility or terminal input modes.
func startLoadingFeedback(ctx context.Context, out io.Writer, label, prompt string, animate bool, animation spinner.Spinner, size func() (int, int)) (stopFeedback func(), advance func(imageLoadingStage)) {
	stop, done := make(chan struct{}), make(chan struct{})
	refresh := make(chan struct{}, 1)
	var stage atomic.Int32
	var once sync.Once
	const clearFrame = "\r\x1b[J"
	prompt = loadingPromptText(prompt)
	started := time.Now()
	go func() {
		defer close(done)
		delay := time.NewTimer(imageLoadingDelay)
		defer delay.Stop()
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-delay.C:
		}
		ticker := time.NewTicker(animation.FPS)
		defer ticker.Stop()
		drawn, reserved, restore, interruptedWrite := false, false, false, false
		clear := func() bool {
			text := clearFrame
			if restore {
				text = "\x1b8" + text
			}
			if interruptedWrite {
				// Cancel a control sequence cut short by a failed write.
				text = "\x18" + text
			}
			n, err := io.WriteString(out, text)
			if err != nil || n != len(text) {
				interruptedWrite = true
				return false
			}
			drawn, restore, interruptedWrite = false, false, false
			return true
		}
		defer func() {
			if drawn {
				// Cleanup must still run after request cancellation.
				canComplete := !interruptedWrite && ctx.Err() == nil && imageLoadingStage(stage.Load()) == imageLoadingSaved
				if clear() && canComplete {
					columns, _ := size()
					if line := loadingStatusLine(imageLoadingSaved, time.Since(started), columns); line != "" {
						// Keep completion visible without retaining the prompt or
						// delaying saving. Later preview failures remain distinct.
						_, _ = fmt.Fprintln(out, line)
					}
				}
			} else if interruptedWrite {
				_, _ = io.WriteString(out, "\x18\r")
			}
		}()
		for frame := 0; ; frame++ {
			// Check before every write, including when a timer and stop are ready.
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			default:
			}
			columns, rows := size()
			current := imageLoadingStage(stage.Load())
			currentLabel := label
			if current == imageLoadingSaving {
				currentLabel = "Saving image"
			} else if current == imageLoadingSaved {
				currentLabel = "Images saved"
			}
			prefix := animation.Frames[frame%len(animation.Frames)] + " "
			text := prefix + loadingPromptLabel(currentLabel, prompt, columns-ansi.StringWidth(prefix))
			if !animate || rows < 2 || columns <= ansi.StringWidth(text) {
				if drawn && !clear() {
					return
				}
				if text := loadingStaticLabel(currentLabel, columns); text != "" {
					_, _ = fmt.Fprintln(out, text)
				}
				return
			}
			if !reserved {
				// Reserve a lower row before saving the anchor, including at
				// the screen bottom where a newline scrolls the terminal.
				const reserve = "\r\n\x1b[A"
				n, err := io.WriteString(out, reserve)
				if err != nil || n != len(reserve) {
					interruptedWrite = n > 0
					return
				}
				reserved = true
			}
			status := loadingStatusLine(current, time.Since(started), columns)
			// A soft wrap keeps both rows together during ordinary terminal
			// reflow. Paint the top last so disabled autowrap safely leaves
			// only that row. No terminal modes or input settings change.
			const anchor = "\r\x1b[J\x1b7"
			text = anchor + fmt.Sprintf("\x1b[%dG  \r", columns) + status + "\x1b8\x1b[2K" + text
			n, err := io.WriteString(out, text)
			drawn = drawn || n > 0
			restore = n >= len(anchor) && n < len(text)
			if err != nil || n != len(text) {
				interruptedWrite = n > 0
				return
			}

			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-refresh:
			}
		}
	}()
	return func() {
			once.Do(func() { close(stop) })
			<-done
		}, func(next imageLoadingStage) {
			if next < imageLoadingWaiting || next > imageLoadingSaved {
				return
			}
			for {
				current := stage.Load()
				if int32(next) <= current {
					return
				}
				if stage.CompareAndSwap(current, int32(next)) {
					break
				}
			}
			select {
			case refresh <- struct{}{}:
			default:
			}
		}
}

// Bound display work without restricting the request. Prepare escapes once,
// then clip this small display copy by terminal cells on each resize.
func loadingPromptText(prompt string) string {
	for i := range prompt {
		if i >= 512 {
			prompt = prompt[:i] + "..."
			break
		}
	}
	prompt = readable.Text(strings.ReplaceAll(prompt, "\\", "\\\\"))
	return strings.NewReplacer("\n", `\n`, "\t", `\t`, "'", `\'`).Replace(prompt)
}

func loadingPromptLabel(label, prompt string, columns int) string {
	available := columns - ansi.StringWidth(label) - 4 // Space, quotes, and a spare terminal cell.
	if prompt == "" || available < 4 {
		return label
	}
	return label + " '" + ansi.Truncate(prompt, available, "...") + "'"
}

// Elapsed time describes the request duration. Saving states come from the
// existing save workflow; no estimated fraction or extra API work is needed.
func loadingStatusLine(stage imageLoadingStage, elapsed time.Duration, columns int) string {
	elapsed = max(elapsed, 0)
	seconds := int64(elapsed / time.Second)
	duration := fmt.Sprintf("%ds", seconds)
	if seconds >= 60 {
		duration = fmt.Sprintf("%dm %02ds", seconds/60, seconds%60)
	}
	labels := []string{duration + " elapsed | Ctrl+C to cancel", duration + " elapsed", duration}
	if stage == imageLoadingSaved {
		labels = []string{"Images saved | " + duration + " elapsed", "Images saved " + duration, "Images saved"}
	}
	for _, label := range labels {
		if len(label) < columns {
			return label
		}
	}
	return ""
}

func loadingStaticLabel(label string, columns int) string {
	if columns <= 0 || len(label) < columns {
		return label
	}
	for _, fallback := range []string{"Working...", "..."} {
		if len(fallback) < columns {
			return fallback
		}
	}
	return ""
}
