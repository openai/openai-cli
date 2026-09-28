package custom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/urfave/cli/v3"
)

const imageLoadingDelay = 400 * time.Millisecond

// Feedback starts after request preparation, so it never changes stdin reading
// or validation. It owns no request/output writers and cannot replace a failure.
func runWithImageLoading(ctx context.Context, command *cli.Command, plan *imageOutputPlan, next cli.ActionFunc) (err error) {
	root := command.Root()
	if plan == nil || !isTerminal(imageCommandWriter(command)) || !isTerminal(os.Stderr) ||
		root.Bool("debug") || errorOutputFormat(root) != "text" || root.String("transform-error") != "" {
		return next(ctx, command)
	}
	label := "Generating image..."
	switch command.Name {
	case "edit":
		label = "Editing image..."
	case "create-variation":
		label = "Creating image variation..."
	}
	parent := ctx
	ctx, stopSignals := signal.NotifyContext(ctx, os.Interrupt)
	// Restore normal signal handling once cancellation starts. A second Ctrl-C
	// can still terminate the process if a terminal write or cleanup is blocked.
	stopReset := context.AfterFunc(ctx, stopSignals)
	stopLoading := startLoadingFeedback(ctx, os.Stderr, label, loadingAnimationSupported(os.Getenv), func() int {
		width, _, _ := term.GetSize(os.Stderr.Fd())
		return width
	})
	plan.stopLoading = stopLoading
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

// startLoadingFeedback returns an idempotent stop that joins its worker. Only
// fixed local labels reach this helper; filenames and API data never do. The
// caller stops it before another writer uses the terminal. Cursor visibility
// and terminal input modes are never changed. Diagnostic writes are best effort.
func startLoadingFeedback(ctx context.Context, out io.Writer, label string, animate bool, width func() int) func() {
	stop, done := make(chan struct{}), make(chan struct{})
	var once sync.Once
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
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		drawn := false
		defer func() {
			if drawn {
				// Cleanup must still run after request cancellation.
				_, _ = io.WriteString(out, "\r\x1b[2K")
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
			columns := width()
			if !animate || columns <= len(label)+2 {
				if drawn {
					if _, err := io.WriteString(out, "\r\x1b[2K"); err != nil {
						return
					}
					drawn = false
				}
				if text := loadingStaticLabel(label, columns); text != "" {
					_, _ = fmt.Fprintln(out, text)
				}
				return
			}
			text := fmt.Sprintf("\r%c %s", "|/-\\"[frame%4], label)
			n, err := io.WriteString(out, text)
			drawn = drawn || n > 0
			if err != nil || n != len(text) {
				return
			}
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		once.Do(func() { close(stop) })
		<-done
	}
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
