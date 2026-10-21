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
	"syscall"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
)

type imagePickerLoadingKey struct{}

// The request owns this read-only copy after the input loop exits. Cleanup runs
// once, before saved paths or previews can write below the transient form.
type imagePickerLoading struct {
	model         imagePicker
	out           io.Writer
	profile       colorprofile.Profile
	once          sync.Once
	content       string
	restoreWrap   bool
	width, height int
	layout        string
	layoutNote    string
	signals       chan os.Signal
}

func newImagePickerLoading(model *imagePicker, out io.Writer, profile colorprofile.Profile) *imagePickerLoading {
	copy := *model
	copy.result = imagePickerResult{}
	copy.page, copy.focus = "settings", "loading"
	copy.selected, copy.optionOffset = 0, 0
	copy.note = ""
	return &imagePickerLoading{model: copy, out: out, profile: profile}
}

func (p *imagePickerLoading) view(stage imageLoadingStage, elapsed time.Duration, width, height int, prefix string) string {
	const statusToken = "__generation_status__"
	if p.layout == "" || p.model.width != width || p.model.height != height || p.layoutNote != p.model.note {
		p.model.width, p.model.height = width, height
		var converted strings.Builder
		_, _ = io.WriteString(&colorprofile.Writer{Forward: &converted, Profile: max(p.profile, colorprofile.ASCII)}, p.summaryView(statusToken))
		p.layout, p.layoutNote = converted.String(), p.model.note
	}
	label := "Generating image"
	if stage == imageLoadingSaving {
		label = "Saving image"
	} else if stage == imageLoadingSaved {
		label = "Images saved"
	}
	status := fmt.Sprintf("%s%s · %ds elapsed", prefix, label, int64(max(elapsed, 0)/time.Second))
	if width < 40 || height < 12 {
		return p.model.fit([]string{status, "Ctrl+C cancel", "Resize for details."}, max(1, width-2), p.model.viewHeight())
	}
	// Cache the submitted summary so spinner ticks do not reprocess the prompt.
	i := strings.LastIndex(p.layout, statusToken)
	if i < 0 {
		return p.layout // A tiny terminal shows resize and cancellation guidance.
	}
	status = ansi.Truncate(status, max(1, min(width-4, 100)-2), "…")
	return p.layout[:i] + status + p.layout[i+len(statusToken):]
}

func (p *imagePickerLoading) summaryView(status string) string {
	m := &p.model
	width, height := max(1, min(m.width-4, 100)), m.viewHeight()
	strong := lipgloss.NewStyle().Bold(true)
	muted, key := lipgloss.NewStyle(), strong
	if m.color {
		text, accent := "#657087", "#3159BC"
		if m.dark {
			text, accent = "#A4ACC2", "#8AA8FF"
		}
		muted = muted.Foreground(lipgloss.Color(text))
		key = key.Foreground(lipgloss.Color(accent))
	}
	// Limit only the displayed excerpt. The request keeps the complete prompt.
	prompt := m.settings.prompt
	for i := range prompt {
		if i >= 512 {
			prompt = prompt[:i] + "…"
			break
		}
	}
	promptLines := strings.Split(ansi.Wrap(imagePickerLine(prompt), width-2, ""), "\n")
	limit := 1
	if height >= 16 {
		limit = 3
	}
	if len(promptLines) > limit {
		promptLines = promptLines[:limit]
		promptLines[limit-1] = ansi.Truncate(promptLines[limit-1], width-3, "") + "…"
	}
	lines := []string{strong.Render("Create image")}
	for _, line := range promptLines {
		lines = append(lines, "  "+line)
	}
	if height >= 16 {
		lines = append(lines, "")
	}
	lines = append(lines, "  "+muted.Render(imagePickerLine(m.settings.model)))
	count := m.settings.count + " image"
	if m.settings.count != "1" {
		count += "s"
	}
	details := strings.ReplaceAll(m.settings.size, "x", "×") + " · " + imagePickerTitle(m.settings.quality) + " quality · " + count
	for _, line := range strings.Split(ansi.Wrap(imagePickerLine(details), width-2, ""), "\n") {
		lines = append(lines, "  "+muted.Render(line))
	}
	if m.note != "" && height >= 16 {
		lines = append(lines, "  "+muted.Render(imagePickerLine(m.note)))
	}
	lines = append(lines, "", "  "+status, "  "+key.Render("[Ctrl+C]")+" "+muted.Render("Cancel"))
	return m.fit(lines, width, height)
}

func (p *imagePickerLoading) draw(stage imageLoadingStage, elapsed time.Duration, width, height int, prefix string) error {
	content := p.view(stage, elapsed, width, height, prefix)
	if content == p.content && width == p.width && height == p.height {
		return nil
	}
	_, err := io.WriteString(p.out, cliterminal.InlineFrame(content, width))
	p.content = content
	p.width, p.height = width, height
	return err
}

func (p *imagePickerLoading) close() {
	if p != nil {
		p.once.Do(func() {
			cleanup := "\r" + ansi.EraseScreenBelow + ansi.SetModeTextCursorEnable
			if p.restoreWrap {
				cleanup += ansi.ResetModeAutoWrap
			}
			_, _ = io.WriteString(p.out, cleanup)
			if p.signals != nil {
				signal.Stop(p.signals)
			}
		})
	}
}

func (p *imagePickerLoading) armSignals() {
	if p.signals == nil {
		p.signals = make(chan os.Signal, 1)
		signal.Notify(p.signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	}
}

// The retained form still owns terminal display state during the request.
// Catch external termination long enough to restore it and preserve shell status.
func (p *imagePickerLoading) request(ctx context.Context, next func(context.Context) error) error {
	if p == nil {
		return next(ctx)
	}
	ctx, cancel := context.WithCancelCause(ctx)
	p.armSignals()
	events := p.signals
	interrupt := func(received os.Signal) {
		// Let another signal force termination if request cleanup blocks.
		signal.Stop(events)
		code := 130
		if received == syscall.SIGTERM {
			code = 143
		} else if received == syscall.SIGHUP {
			code = 129
		}
		cancel(&imagePickerGenerationInterrupt{error: context.Canceled, code: code})
	}
	// A signal can arrive while the submitted draft is being saved, before
	// request preparation begins. Consume it before calling the API action.
	select {
	case received := <-events:
		interrupt(received)
	default:
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case received := <-events:
			interrupt(received)
		case <-ctx.Done():
		}
	}()
	err := ctx.Err()
	if err == nil {
		err = next(ctx)
	}
	cause := context.Cause(ctx)
	signal.Stop(events)
	cancel(nil)
	<-done
	var interrupted *imagePickerGenerationInterrupt
	if errors.As(cause, &interrupted) && (err == nil || errors.Is(err, context.Canceled)) {
		return &imagePickerGenerationInterrupt{error: errors.Join(err, context.Canceled), code: interrupted.code}
	}
	return err
}

type imagePickerGenerationInterrupt struct {
	error
	code int
}

func (e *imagePickerGenerationInterrupt) Unwrap() error { return e.error }
func (e *imagePickerGenerationInterrupt) ExitCode() int { return e.code }
