package images

import (
	"bytes"
	"context"
	"errors"
	"image"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/term"
	localcommands "github.com/openai/openai-cli/internal/commands"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"github.com/openai/openai-cli/internal/terminalimage"
	"github.com/urfave/cli/v3"
)

type interactiveImageExit struct{ code int }

func (e interactiveImageExit) Error() string { return "interactive image viewer closed" }

type interactiveImageFailure struct {
	cause error
	code  int
}

func (e *interactiveImageFailure) Error() string {
	return "Could not display or restore the interactive preview. The original file is unchanged."
}
func (e *interactiveImageFailure) Unwrap() error {
	return &localcommands.LocalError{Message: e.Error(), Cause: e.cause}
}
func (e *interactiveImageFailure) ExitCode() int { return e.code }

// The viewer already attempted this diagnostic through a bounded writer.
// Preserve the cause and status without retrying through main's ordinary sink.
type reportedInteractiveImageFailure struct {
	cause error
	code  int
}

func (e *reportedInteractiveImageFailure) Error() string { return "" }
func (e *reportedInteractiveImageFailure) Unwrap() error { return e.cause }
func (e *reportedInteractiveImageFailure) ExitCode() int { return e.code }

func interactiveImageEligible(input, output bool, getenv func(string) string, goos string) bool {
	if !input || !output || (goos != "darwin" && goos != "linux") || getenv("TERM_PROGRAM") != "" {
		return false
	}
	terminal, version := getenv("TERM"), getenv("KONSOLE_VERSION")
	ci := strings.ToLower(getenv("CI"))
	if terminal == "dumb" || terminal == "xterm-kitty" || terminal == "xterm-ghostty" ||
		(ci != "" && ci != "0" && ci != "false") || getenv("TMUX") != "" || getenv("STY") != "" ||
		getenv("ZELLIJ") != "" || strings.HasPrefix(terminal, "screen") || strings.HasPrefix(terminal, "tmux") || len(version) != 6 {
		return false
	}
	for _, digit := range version {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	number, err := strconv.Atoi(version)
	return err == nil && number >= 220400
}

func handleInteractiveImagePreview(parent context.Context, command *cli.Command, opts PreviewOptions) (resultErr error) {
	if command.IsSet("inline") {
		return imageSavingFailure("Use --interactive by itself; --inline selects a static preview.", nil)
	}
	input, _ := command.Root().Reader.(*os.File)
	output, _ := imageCommandWriter(command).(*os.File)
	if !interactiveImageEligible(input != nil && cliterminal.IsWriterTerminal(input), output != nil && cliterminal.IsWriterTerminal(output), os.Getenv, runtime.GOOS) {
		return imageSavingFailure("Interactive previews require Konsole 22.04 or newer, terminal input and output, and a macOS or Linux CLI. Run outside CI and multiplexers.", nil)
	}
	path, err := filepath.Abs(command.Args().First())
	if err != nil {
		return imageSavingFailure("Could not resolve the image path.", err)
	}
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	signalDone := make(chan struct{})
	go func() {
		defer close(signalDone)
		select {
		case <-ctx.Done():
		case received := <-signals:
			code := 130
			if received == syscall.SIGTERM {
				code = 143
			} else if received == syscall.SIGHUP {
				code = 129
			}
			cancel(interactiveImageExit{code})
		}
	}()
	defer func() { cancel(nil); <-signalDone }()
	ctx, closeOutput := terminalimage.PrepareInteractiveImageOutput(ctx, output)
	var reporter *cliterminal.Output
	defer func() {
		resultErr = finishInteractiveImagePreview(ctx, errors.Join(resultErr, closeOutput()))
		if reporter != nil {
			resultErr = reportInteractiveImageFailure(command, reporter, resultErr, opts)
		}
	}()
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	// Bind stderr before the user can remove or replace the executable. This
	// resident writer also survives foreground-group signals during the view.
	reporter, err = cliterminal.NewOutput(cliterminal.DetachedOutputContext(ctx), executable, os.Stderr)
	if err != nil {
		return err
	}
	// Read one immutable snapshot. Resizing never reopens or changes the file.
	img, err := terminalimage.ReadSaved(ctx, path)
	if err == nil {
		err = runInteractiveImagePreview(ctx, cancel, input, output, img)
	}
	return err
}

func finishInteractiveImagePreview(ctx context.Context, err error) error {
	code := 1
	var stopped interactiveImageExit
	if errors.As(context.Cause(ctx), &stopped) {
		err = withoutInteractiveImageCancellation(err)
		if err == nil {
			if stopped.code != 0 {
				return cli.Exit("", stopped.code)
			}
			return nil
		}
		if stopped.code != 0 {
			code = stopped.code
		}
	}
	if err != nil {
		return &interactiveImageFailure{cause: err, code: code}
	}
	return nil
}

func reportInteractiveImageFailure(command *cli.Command, output *cliterminal.Output, failure error, opts PreviewOptions) (result error) {
	defer func() {
		if err := output.Close(); err != nil {
			code := 1
			var exit cli.ExitCoder
			if errors.As(result, &exit) {
				code = exit.ExitCode()
			}
			result = &reportedInteractiveImageFailure{cause: errors.Join(result, err), code: code}
		}
	}()
	if failure == nil {
		return nil
	}
	var exit cli.ExitCoder
	code := 1
	if errors.As(failure, &exit) {
		if exit.Error() == "" {
			return failure
		}
		code = exit.ExitCode()
	}
	var diagnostic bytes.Buffer
	err := opts.ShowError(command.Root(), failure, &diagnostic)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		_, err = output.WriteContext(ctx, diagnostic.Bytes())
	}
	return &reportedInteractiveImageFailure{cause: errors.Join(failure, err), code: code}
}

type interactiveImageSize struct{ width, height, cellWidth, cellHeight int }

func interactiveImageViewport(output *os.File) (interactiveImageSize, error) {
	width, height, err := term.GetSize(output.Fd())
	cw, ch := terminalimage.CellSize(output.Fd())
	return interactiveImageSize{width, height, cw, ch}, err
}

func runInteractiveImagePreview(ctx context.Context, cancel context.CancelCauseFunc, input, output *os.File, img image.Image) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return interactiveImageWriteError(ctx, err)
	}
	inputState, err := term.GetState(input.Fd())
	if err != nil {
		return err
	}
	outputState, err := term.GetState(output.Fd())
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, term.Restore(input.Fd(), inputState), term.Restore(output.Fd(), outputState))
	}()
	if _, err := term.MakeRaw(input.Fd()); err != nil {
		return err
	}
	source, err := uv.NewCancelReader(input)
	if err != nil {
		return err
	}
	inputDone := make(chan struct{})
	readerStopped := make(chan struct{})
	stopReader := context.AfterFunc(ctx, func() { defer close(readerStopped); source.Cancel() })
	go func() {
		defer close(inputDone)
		readInteractiveImageKeys(ctx, source, cancel)
	}()
	defer func() {
		cancel(nil)
		source.Cancel()
		<-inputDone
		if !stopReader() {
			<-readerStopped
		}
		resultErr = errors.Join(resultErr, source.Close())
	}()
	// All output, including recovery, uses the bound native worker. Never write
	// directly to a potentially blocked terminal from this process.
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 250*time.Millisecond)
		defer stop()
		resultErr = errors.Join(resultErr, terminalimage.WriteControl(cleanup, output, "\x1b\\\x1b[?1049l\x1b[?2004r\x1b[?25r"))
	}()
	if err := terminalimage.WriteControl(ctx, output, "\x1b[?25s\x1b[?2004s\x1b[?1049h\x1b[?25l\x1b[?2004h"); err != nil {
		return interactiveImageWriteError(ctx, err)
	}
	resize := time.NewTicker(75 * time.Millisecond)
	defer resize.Stop()
	var painted interactiveImageSize
	first := true
	for {
		current, err := interactiveImageViewport(output)
		if err != nil {
			return err
		}
		if first || current != painted {
			// Writes remain serial. A resize during transfer schedules one new
			// frame at the latest size, without retiring a healthy output worker.
			if err := paintInteractiveImage(ctx, output, img, current); err != nil {
				return interactiveImageWriteError(ctx, err)
			}
			painted, first = current, false
			continue
		}
		select {
		case <-ctx.Done():
			return interactiveImageWriteError(ctx, ctx.Err())
		case <-resize.C:
		}
	}
}

func interactiveImageWriteError(ctx context.Context, err error) error {
	var stopped interactiveImageExit
	if errors.As(context.Cause(ctx), &stopped) && errors.Is(err, context.Canceled) {
		return withoutInteractiveImageCancellation(err)
	}
	if ctx.Err() != nil {
		return errors.Join(withoutInteractiveImageCancellation(err), context.Cause(ctx))
	}
	return err
}

// Cancellation of an output job can accompany a separate write or reset
// failure. Remove only cancellation leaves, retaining every other cause.
func withoutInteractiveImageCancellation(err error) error {
	if err == nil || err == context.Canceled {
		return nil
	}
	if !errors.Is(err, context.Canceled) {
		return err
	}
	if _, canceled := err.(*terminalimage.CanceledOutputError); canceled {
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var retained []error
		for _, cause := range joined.Unwrap() {
			retained = append(retained, withoutInteractiveImageCancellation(cause))
		}
		return errors.Join(retained...)
	}
	if errors.Is(err, context.Canceled) {
		if wrapped, ok := err.(interface{ Unwrap() error }); ok {
			return withoutInteractiveImageCancellation(wrapped.Unwrap())
		}
	}
	return err
}

func paintInteractiveImage(ctx context.Context, output io.Writer, img image.Image, size interactiveImageSize) error {
	columns := savedImagePreviewColumns(img.Bounds(), size.width, size.height-1, size.cellWidth, size.cellHeight)
	hint := "q / Esc: close"
	if columns < 1 {
		hint = "Enlarge window. q: close"
	}
	hint = hint[:min(len(hint), max(0, size.width-1))]
	if err := terminalimage.WriteControl(ctx, output, "\x1b[2J\x1b[H"+hint); err != nil {
		return err
	}
	if columns < 1 {
		return nil
	}
	if err := terminalimage.WriteControl(ctx, output, "\x1b[2;1H"); err != nil {
		return err
	}
	return terminalimage.Write(ctx, output, img, "iterm-konsole", columns)
}
