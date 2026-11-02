package tokenize

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
	localcommands "github.com/openai/openai-cli/internal/commands"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/readable"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"github.com/openai/openai-cli/internal/tokenizer"
	"github.com/urfave/cli/v3"
)

func handleTokenizerEditor(ctx context.Context, command *cli.Command) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	view := command.Name == "view"
	if command.Args().Present() {
		if view {
			return &localcommands.LocalError{Message: "Tokenizer view reads standard input. Pipe UTF-8 text into the command or redirect a file."}
		}
		return &localcommands.LocalError{Message: "Tokenizer takes no positional input. Open the editor without arguments, or use tokenizer count --text TEXT."}
	}
	format, err := tokenizerOutputFormat(command)
	if view && (err != nil || format != "auto") {
		return &localcommands.LocalError{Message: "Tokenizer view requires --format auto without --transform or --raw-output. Use tokenizer count or tokenizer inspect for script output."}
	}
	if err != nil {
		return &localcommands.LocalError{Message: "The tokenizer editor supports --format auto or text. For JSON, use tokenizer count or inspect. --transform and --raw-output are unsupported."}
	}
	if format == "json" {
		return &localcommands.LocalError{Message: "Choose tokenizer count or tokenizer inspect for --format json. Use --text, --file, or piped input."}
	}
	root := command.Root()
	input, _ := root.Reader.(*os.File)
	output, _ := root.Writer.(*os.File)
	invocation, _ := root.Metadata["help-invocation"].(string)
	if invocation == "" {
		invocation = "openai"
	}
	terminalInput := input != nil && term.IsTerminal(input.Fd())
	display := output != nil && cliterminal.AutomaticPresentation(format, term.IsTerminal(output.Fd()), os.Getenv)
	if view && !display {
		return &localcommands.LocalError{Message: "Tokenizer view requires interactive terminal output in automatic mode. Use tokenizer count or tokenizer inspect for scripts."}
	}
	if !view && (!display || !terminalInput) {
		_, err := fmt.Fprintf(clioutput.Writer{Context: ctx, Sink: root.Writer},
			"Tokenizer\n\nOpen the live editor with terminal input and output:\n  %s tokenizer\n\n"+
				"Commands\n  view       Explore piped text in the editor\n  count      Count exact plain-text tokens\n  inspect    Show token IDs and byte boundaries\n"+
				"  encodings  List available encodings\n  licenses   Show bundled notices\n\n"+
				"Explore a Git diff in the editor\n  git diff | %[1]s tokenizer view\n\n"+
				"Pipe UTF-8 text into a script command\n  echo hello | %[1]s tokenizer count --transform token_count\n  echo hello | %[1]s tokenizer inspect --format json\n", readable.Text(invocation))
		return tokenizerOutputFailure(err)
	}
	var draft string
	if !terminalInput {
		// Prove the display can supply a keyboard before consuming the producer.
		input, err = cliterminal.OpenInputFromOutput(output)
		if err != nil {
			return &localcommands.LocalError{Message: "Tokenizer view needs a readable terminal for keyboard input. Use tokenizer inspect for script output.", Cause: err}
		}
		defer input.Close()
		draft, err = readTokenizerInput(ctx, command)
		if err != nil {
			return err
		}
	}
	code, err := runTokenizerEditor(ctx, input, output, invocation, draft)
	if err != nil {
		return &localcommands.LocalError{Message: "Could not display the tokenizer editor. Use tokenizer count or tokenizer inspect with --text or --file.", Cause: err}
	}
	if code != 0 {
		return cli.Exit("", code)
	}
	return nil
}

func runTokenizerEditor(parent context.Context, input, output *os.File, invocation, draft string) (code int, err error) {
	if err := parent.Err(); err != nil {
		return 0, err
	}
	width, height, err := term.GetSize(output.Fd())
	if err != nil {
		return 0, err
	}
	executable, err := os.Executable()
	if err != nil {
		return 0, err
	}
	terminalOutput, err := cliterminal.NewOutput(parent, executable, output)
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, terminalOutput.Close()) }()
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
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	// Restore both sides even when console setup changes one side then fails.
	defer func() {
		err = errors.Join(err, term.Restore(input.Fd(), inputState), term.Restore(output.Fd(), outputState))
	}()
	console := uv.NewConsole(input, output, os.Environ())
	if _, err := console.MakeRaw(); err != nil {
		return 0, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inputBridge, err := newTokenizerInputBridge(ctx, input)
	if err != nil {
		return 0, err
	}
	defer inputBridge.Close()
	worker := newTokenizerPreviewWorker(ctx, executable)
	defer worker.Close()
	painter := cliterminal.NewPainter(terminalOutput, cancel)
	defer painter.Stop()
	model := newTokenizerEditor()
	model.width, model.height = width, height
	if draft != "" {
		model.insert(draft)
		model.cursor, model.focus = 0, tokenizerFocusResults
		if len(draft) > tokenizerInlineBytes {
			worker.Replace(model.revision, draft, model.encoding)
		}
	}
	model.dark = cliterminal.DarkBackground(os.Getenv("COLORFGBG"))
	if invocation != "" {
		model.invocation = invocation
	}
	profile := cliterminal.ColorProfile(output, os.Environ(), term.IsTerminal(output.Fd()))
	model.color = profile >= colorprofile.ANSI
	inline := &tokenizerEditorInline{model: model, worker: worker, output: output, profile: profile,
		painter: painter, input: inputBridge, fail: cancel}
	options := []tea.ProgramOption{tea.WithInput(inputBridge.Input()), tea.WithOutput(io.Discard), tea.WithContext(ctx),
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
				} else if received == syscall.SIGHUP {
					code = 129
				}
				program.Send(tokenizerEditorStopMsg{Code: code})
				return
			case <-ctx.Done():
				return
			case <-parent.Done():
				program.Send(tokenizerEditorStopMsg{Code: 130})
				return
			case <-inputBridge.Done():
				if err := inputBridge.Err(); err != nil {
					program.Send(tokenizerEditorInputErrorMsg{err: err})
				}
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
	inputErr := inputBridge.Close()
	writeErr := painter.Stop()
	cleanupErr := cliterminal.CloseFrame(terminalOutput, inline.close(), model.exitCode != 0)
	if (writeErr != nil || inline.err != nil) && errors.Is(runErr, context.Canceled) {
		// A failed write cancels the private loop, not the user's operation.
		runErr = nil
	}
	return model.exitCode, errors.Join(runErr, writeErr, inline.err, closeErr, inputErr, cleanupErr, parent.Err())
}

type tokenizerEditorSizeErrorMsg struct{}
type tokenizerEditorWrapTimeout struct{}
type tokenizerEditorInputErrorMsg struct{ err error }

type tokenizerEncodingPrepared struct {
	encoding string
	err      error
}

// Bound synchronous BPE work, including a single unbroken word. Larger drafts use the cancellable child.
const tokenizerInlineBytes = 4 << 10

// Reuse the image picker's frame layout while keeping terminal backpressure
// outside the model and retaining the tokenizer's exact input bytes.
type tokenizerEditorInline struct {
	model                             *tokenizerEditor
	worker                            *tokenizerPreviewWorker
	output                            *os.File
	painter                           cliterminal.FramePainter
	input                             *tokenizerInputBridge
	fail                              context.CancelFunc
	profile                           colorprofile.Profile
	started, modesActive, restoreWrap bool
	preparing                         bool
	prepared                          map[string]error
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
	p.painter.Control(query)
	wrap := tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return tokenizerEditorWrapTimeout{} })
	return tea.Batch(wrap, p.prepareEncoding())
}

// Warm at most one encoding at a time while the editor continues to accept input.
func (p *tokenizerEditorInline) prepareEncoding() tea.Cmd {
	if p.preparing {
		return nil
	}
	p.preparing = true
	encoding := p.model.encoding
	return func() tea.Msg {
		return tokenizerEncodingPrepared{encoding, tokenizer.Prepare(encoding)}
	}
}

func (p *tokenizerEditorInline) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if p.input != nil {
		message = p.input.Resolve(message)
	}
	warmed := false
	switch msg := message.(type) {
	case tokenizerEditorInputErrorMsg:
		p.err = msg.err
		return p, tea.Quit
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
	case tokenizerEncodingPrepared:
		p.preparing = false
		if p.prepared == nil {
			p.prepared = make(map[string]error)
		}
		p.prepared[msg.encoding] = msg.err
		warmed = true
	}
	revision := p.model.revision
	_, cmd := p.model.Update(message)
	if p.model.quit {
		p.worker.Cancel()
	} else if revision != p.model.revision || warmed && len(p.model.text) <= tokenizerInlineBytes {
		cmd = p.refreshPreview()
	}
	// Show the first editor frame once its encoding can count the first keystroke.
	// Input and cancellation stay active while the initial vocabulary loads.
	_, ready := p.prepared[p.model.encoding]
	if p.started && !p.model.quit && (p.content != "" || ready || len(p.model.text) > tokenizerInlineBytes) {
		p.draw()
	}
	return p, cmd
}

func (p *tokenizerEditorInline) refreshPreview() tea.Cmd {
	m := p.model
	if len(m.text) > tokenizerInlineBytes {
		p.worker.Replace(m.revision, m.text, m.encoding)
		return nil
	}
	p.worker.Cancel()
	err, ready := p.prepared[m.encoding]
	if !ready {
		return p.prepareEncoding()
	}
	if m.updating {
		preview := tokenizerEditorResultMsg{Revision: m.revision, Err: err}
		if err == nil {
			result, err := tokenizer.Encode(m.text, m.encoding, true)
			preview.Err = err
			var end uint32
			preview.Tokens = make([]tokenizerPreviewToken, len(result.IDs))
			for i, id := range result.IDs {
				end += uint32(len(result.Fragments[i]))
				preview.Tokens[i] = tokenizerPreviewToken{ID: uint32(id), EndByte: end}
			}
		}
		m.Update(preview)
	}
	return nil
}

func (p *tokenizerEditorInline) View() tea.View { return tea.NewView("") }

func (p *tokenizerEditorInline) start() {
	if p.started || p.err != nil {
		return
	}
	p.started = true
	p.painter.Control(ansi.SetModeAutoWrap)
}

func (p *tokenizerEditorInline) draw() {
	width, height, err := term.GetSize(p.output.Fd())
	if err != nil {
		p.err = errors.New("could not read terminal size")
		p.fail()
		return
	}
	p.drawFrame(width, height)
}

func (p *tokenizerEditorInline) drawFrame(width, height int) {
	p.model.width, p.model.height = width, height
	var converted strings.Builder
	_, _ = io.WriteString(&colorprofile.Writer{Forward: &converted, Profile: max(p.profile, colorprofile.ASCII)}, p.model.View().Content)
	content := converted.String()
	if content == p.content && width == p.width && height == p.height {
		return
	}
	p.painter.Frame(cliterminal.InlineFrame(content, width))
	p.content, p.width, p.height = content, width, height
}

func (p *tokenizerEditorInline) close() string {
	if !p.modesActive {
		return ""
	}
	// Cancel a partial escape sequence before restoring visible terminal modes.
	cleanup := "\x18\x1b\\" + ansi.ResetStyle
	if p.width > 0 {
		cleanup += "\r" + ansi.EraseScreenBelow
	}
	cleanup += ansi.ResetModeBracketedPaste + ansi.SetModeTextCursorEnable
	if p.restoreWrap {
		cleanup += ansi.ResetModeAutoWrap
	}
	p.started, p.modesActive = false, false
	return cleanup
}
