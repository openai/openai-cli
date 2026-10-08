package custom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/urfave/cli/v3"
)

func handleTokenizerEditor(ctx context.Context, command *cli.Command) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if command.Args().Present() {
		return &localUtilityError{message: "Tokenizer takes no positional input. Open the editor without arguments, or use tokenizer count --text TEXT."}
	}
	format, err := tokenizerOutputFormat(command)
	if err != nil {
		return &localUtilityError{message: "The tokenizer editor supports --format auto or text. For JSON, use tokenizer count or inspect. --transform and --raw-output are unsupported."}
	}
	if format == "json" {
		return &localUtilityError{message: "Choose tokenizer count or tokenizer inspect for --format json. Use --text, --file, or piped input."}
	}
	root := command.Root()
	input, _ := root.Reader.(*os.File)
	output, _ := root.Writer.(*os.File)
	invocation, _ := root.Metadata["help-invocation"].(string)
	if invocation == "" {
		invocation = "openai"
	}
	if input == nil || output == nil || !isTerminal(input) ||
		!codexTerminalGuideEligible(format, isTerminal(output), os.Getenv) {
		_, err := fmt.Fprintf(outputWriter{ctx: ctx, out: root.Writer},
			"Tokenizer\n\nOpen the live editor with terminal input and output:\n  %s tokenizer\n\n"+
				"Commands\n  count      Count exact plain-text tokens\n  inspect    Show token IDs and byte boundaries\n"+
				"  encodings  List available encodings\n  licenses   Show bundled notices\n\n"+
				"For count or inspect, use --text, --file, or piped input.\n"+
				"Use --format json for structured results. Add --help for examples.\n", readable.Text(invocation))
		return tokenizerOutputFailure(err)
	}
	code, err := runTokenizerEditor(ctx, input, output, invocation)
	if err != nil {
		return &localUtilityError{message: "Could not display the tokenizer editor. Use tokenizer count or tokenizer inspect with --text or --file.", cause: err}
	}
	if code != 0 {
		return cli.Exit("", code)
	}
	return nil
}

func runTokenizerEditor(parent context.Context, input, output *os.File, invocation string) (code int, err error) {
	if err := parent.Err(); err != nil {
		return 0, err
	}
	width, height, err := term.GetSize(output.Fd())
	if err != nil {
		return 0, err
	}
	inputState, err := term.GetState(input.Fd())
	if err != nil {
		return 0, err
	}
	outputState, err := term.GetState(output.Fd())
	if err != nil {
		return 0, err
	}
	// Catch signals before raw mode starts, and release them after restoration.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	// Restore both sides even when console setup changes one side then fails.
	defer func() {
		err = errors.Join(err, term.Restore(input.Fd(), inputState), term.Restore(output.Fd(), outputState))
	}()
	console := uv.NewConsole(input, output, os.Environ())
	if _, err := console.MakeRaw(); err != nil {
		return 0, err
	}
	executable, err := os.Executable()
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := newTokenizerPreviewWorker(ctx, executable)
	defer worker.Close()
	model := newTokenizerEditor()
	model.width, model.height = width, height
	model.dark = codexGuideDarkBackground(os.Getenv("COLORFGBG"))
	if invocation != "" {
		model.invocation = invocation
	}
	tracked := &imagePickerOutput{File: output, cancel: cancel}
	profile := prettyColorProfile(output, os.Environ())
	model.color = profile >= colorprofile.ANSI
	inline := &tokenizerEditorInline{model: model, worker: worker, output: tracked, profile: profile}
	options := []tea.ProgramOption{tea.WithInput(input), tea.WithOutput(tracked), tea.WithContext(ctx),
		tea.WithWindowSize(width, height), tea.WithColorProfile(profile), tea.WithoutSignalHandler(), tea.WithoutRenderer()}
	program := tea.NewProgram(inline, options...)
	listenerDone := make(chan struct{})
	defer func() {
		cancel()
		<-listenerDone
	}()
	go func() {
		defer close(listenerDone)
		resize := time.NewTicker(100 * time.Millisecond)
		defer resize.Stop()
		for {
			select {
			case received := <-signals:
				code := 130
				if received == syscall.SIGTERM {
					code = 143
				}
				program.Send(tokenizerEditorStopMsg{Code: code})
				return
			case <-ctx.Done():
				return
			case <-parent.Done():
				program.Send(tokenizerEditorStopMsg{Code: 130})
				return
			case result, ok := <-worker.Results():
				if !ok {
					return
				}
				program.Send(tokenizerEditorResultMsg{Revision: result.Revision, Tokens: result.Tokens, Err: result.Err})
			case <-resize.C:
				w, h, err := term.GetSize(output.Fd())
				if err != nil {
					program.Send(tokenizerEditorSizeErrorMsg{})
					return
				}
				if w != width || h != height {
					width, height = w, h
					program.Send(tea.WindowSizeMsg{Width: w, Height: h})
				}
			}
		}
	}()
	_, runErr := program.Run()
	closeErr := worker.Close()
	inline.close()
	writeErr := tracked.Err()
	if writeErr != nil && errors.Is(runErr, context.Canceled) {
		// A failed write cancels the private loop, not the user's operation.
		runErr = nil
	}
	return model.exitCode, errors.Join(runErr, writeErr, inline.err, closeErr, parent.Err())
}

type tokenizerEditorSizeErrorMsg struct{}
type tokenizerEditorWrapTimeout struct{}

// The image picker owns the shared frame and failed-write primitives. This
// wrapper adds revision-bound preview work without changing the image session.
type tokenizerEditorInline struct {
	model                             *tokenizerEditor
	worker                            *tokenizerPreviewWorker
	output                            *imagePickerOutput
	profile                           colorprofile.Profile
	started, modesActive, restoreWrap bool
	content                           string
	width, height                     int
	err                               error
}

func (p *tokenizerEditorInline) Init() tea.Cmd {
	p.modesActive = true
	query := ansi.ResetModeTextCursorEnable + ansi.SetModeBracketedPaste + ansi.RequestModeAutoWrap
	if p.model.color {
		query += ansi.RequestBackgroundColor
	}
	_, _ = io.WriteString(p.output, query)
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return tokenizerEditorWrapTimeout{} })
}

func (p *tokenizerEditorInline) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tokenizerEditorSizeErrorMsg:
		p.err = errors.New("could not read terminal size")
		return p, tea.Quit
	case tea.ModeReportMsg:
		if msg.Mode == ansi.ModeAutoWrap {
			if msg.Value == ansi.ModePermanentlyReset {
				p.err = errors.New("terminal line wrapping is unavailable")
				return p, tea.Quit
			}
			p.restoreWrap = msg.Value == ansi.ModeReset
			p.start()
		}
	case tokenizerEditorWrapTimeout:
		p.start()
	case tea.ColorProfileMsg:
		p.profile = msg.Profile
	case tokenizerEditorRequestMsg:
		if msg.Revision == p.model.revision && p.model.updating && !p.model.quit {
			p.worker.Replace(msg.Revision, msg.Text, msg.Encoding)
		}
		return p, nil
	}
	revision := p.model.revision
	_, cmd := p.model.Update(message)
	if revision != p.model.revision || p.model.quit {
		p.worker.Cancel()
	}
	if p.started {
		p.draw()
	}
	return p, cmd
}

func (p *tokenizerEditorInline) View() tea.View { return tea.NewView("") }

func (p *tokenizerEditorInline) start() {
	if p.started || p.err != nil {
		return
	}
	p.started = true
	_, _ = io.WriteString(p.output, ansi.SetModeAutoWrap)
}

func (p *tokenizerEditorInline) draw() {
	width, height, err := term.GetSize(p.output.Fd())
	if err != nil {
		p.err = errors.New("could not read terminal size")
		p.output.cancel()
		return
	}
	p.model.width, p.model.height = width, height
	var converted strings.Builder
	_, _ = io.WriteString(&colorprofile.Writer{Forward: &converted, Profile: max(p.profile, colorprofile.ASCII)}, p.model.View().Content)
	content := converted.String()
	if content == p.content && width == p.width && height == p.height {
		return
	}
	_, _ = io.WriteString(p.output, imagePickerInlineFrame(content, width))
	p.content, p.width, p.height = content, width, height
}

func (p *tokenizerEditorInline) close() {
	if !p.modesActive {
		return
	}
	cleanup := ansi.ResetModeBracketedPaste + ansi.SetModeTextCursorEnable
	if p.started {
		cleanup = "\r" + ansi.EraseScreenBelow + cleanup
	}
	if p.restoreWrap {
		cleanup += ansi.ResetModeAutoWrap
	}
	_, _ = io.WriteString(p.output, cleanup)
	p.started, p.modesActive = false, false
}
