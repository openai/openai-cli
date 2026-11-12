package audio

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/audiolive"
	"github.com/openai/openai-cli/internal/audiomedia"
	"github.com/openai/openai-cli/internal/modeldefaults"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/readable"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"github.com/urfave/cli/v3"
)

type terminalTranslationTick struct{}

// The UI polls one current snapshot. Slow terminal output cannot block capture
// or accumulate transcript messages. Complete captions stay in owned files.
type terminalTranslationModel struct {
	ctx                                        context.Context
	cancel                                     context.CancelFunc
	options                                    terminalTranslationOptions
	form                                       terminalTranslationForm
	transcript                                 *translationTranscript
	translateText                              terminalTextTranslator
	cancelRun, cancelReplay                    context.CancelFunc
	replayDone                                 chan struct{}
	replayErr                                  error
	seekPosition                               time.Duration
	folder                                     string
	width, height                              int
	color, dark                                bool
	retrySetup                                 bool
	started, finished, stopping, saving, saved bool
	state                                      terminalTranslationUpdate
	note, savedPath                            string
	lastErr                                    error
	exitCode                                   int
	stop                                       chan struct{}
	done, saveDone                             chan struct{}
	workers                                    sync.WaitGroup
	mu                                         sync.Mutex
	latest                                     terminalTranslationUpdate
	result                                     terminalTranslationResult
	savePath                                   string
	saveErr                                    error
	validateCapture                            func(string) error
	validatePlayback                           func() error
}

func newTerminalTranslationModel(ctx context.Context, cancel context.CancelFunc, command *cli.Command, connect func(context.Context, audiolive.Config) (audiolive.Session, error), translateText terminalTextTranslator) *terminalTranslationModel {
	m := &terminalTranslationModel{
		ctx: ctx, cancel: cancel, folder: command.String("output-dir"),
		form: terminalTranslationForm{textModel: modeldefaults.TranslationText, path: command.String("file")}, translateText: translateText,
		options: terminalTranslationOptions{
			Config: audiolive.Config{Language: command.String("target-language"), NoiseReduction: command.Bool("noise-reduction"), Model: modeldefaults.Translation, TranscriptionModel: modeldefaults.TranslationTranscript},
			Device: command.String("input-device"), Listen: !command.Bool("no-playback"),
			Connect: connect, Capture: audiomedia.CapturePCM, Play: audiomedia.PlayPCM,
		},
		validateCapture: audiomedia.ValidateCapture, validatePlayback: audiomedia.ValidatePlayback,
	}
	if m.form.path != "" {
		m.form.source = translationFile
	}
	m.focusSetupAction()
	return m
}
func (m *terminalTranslationModel) Init() tea.Cmd             { return nil }
func (m *terminalTranslationModel) audioGeometry() (int, int) { return m.width, m.height }
func (m *terminalTranslationModel) audioUsesColor() bool      { return m.color }
func terminalTranslationPoll() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return terminalTranslationTick{} })
}
func (m *terminalTranslationModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.transcript != nil {
			m.transcript.starts = []int64{0}
			m.readTranscript()
		}
		if edit := m.form.editor; edit != nil {
			m.sizeTranslationEditor(edit)
		}
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
	case terminalTranslationTick:
		if m.started && !m.finished {
			m.mu.Lock()
			m.state = m.latest
			m.mu.Unlock()
			select {
			case <-m.done:
				// done publishes the final snapshot and sealed artifacts together.
				m.mu.Lock()
				m.state = m.latest
				m.mu.Unlock()
				m.finished = true
				m.cancelRun()
				m.lastErr = m.result.Err
				if m.stopping && errors.Is(m.lastErr, context.Canceled) {
					m.lastErr = nil
				}
				if m.lastErr != nil {
					if !m.hasResult() && m.state.Translation == "" {
						// Keep the form editable when a failed request produced no output.
						failure := m.lastErr
						cleanupErr := m.resetSession()
						m.lastErr = errors.Join(failure, cleanupErr)
						m.retrySetup = cleanupErr == nil
					}
					m.note = m.lastErr.Error()
				}
			default:
			}
		}
		if m.saving {
			select {
			case <-m.saveDone:
				m.saving = false
				if m.saveErr != nil {
					m.note = m.saveErr.Error()
					m.lastErr = m.saveErr
				} else {
					m.saved = true
					m.savedPath = m.savePath
					m.note = ""
					m.lastErr = m.result.Err
				}
			default:
			}
		}
		if m.replayDone != nil {
			select {
			case <-m.replayDone:
				m.cancelReplay()
				m.replayDone = nil
				if m.replayErr != nil && !errors.Is(m.replayErr, context.Canceled) {
					m.lastErr = m.replayErr
					m.note = "Could not replay audio. Save the recording or check FFplay."
				}
			default:
			}
		}
		if m.started && !m.finished || m.saving || m.replayDone != nil {
			return m, terminalTranslationPoll()
		}
	case tea.PasteMsg:
		if m.form.editor != nil {
			return m, m.updateField(msg)
		}
	case tea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+c" {
			m.exitCode = 130
			m.cancel()
			return m, tea.Quit
		}
		if m.form.editor != nil {
			return m, m.updateField(msg)
		}
		if !m.started && m.form.languagesOpen && key == "esc" {
			return m, m.setupKey(key)
		}
		if m.transcript != nil {
			m.transcriptKey(key)
			return m, nil
		}
		if key == "esc" || key == "q" {
			if m.started && !m.finished {
				m.stopCapture()
				return m, nil
			}
			if m.saving {
				return m, nil
			}
			return m, tea.Quit
		}
		if key == "space" && m.started && !m.finished {
			m.stopCapture()
			return m, nil
		}
		if m.width < 40 || m.height < 16 {
			return m, nil
		}
		if !m.started {
			return m, m.setupKey(key)
		}
		if key == "t" && m.finished && m.hasResult() {
			m.openTranscript(false)
			return m, nil
		}
		if (key == "left" || key == "right") && m.finished && m.result.AudioBytes > 0 && m.replayDone == nil {
			step := 500 * time.Millisecond
			if key == "left" {
				step = -step
			}
			m.seekPosition = max(0, min(m.seekPosition+step, m.result.duration()))
			return m, nil
		}
		if key == "p" && m.finished && m.result.AudioBytes > 0 {
			if m.replayDone != nil {
				m.cancelReplay()
				return m, nil
			}
			replayCtx, cancel := context.WithCancel(m.ctx)
			m.cancelReplay = cancel
			m.replayDone = make(chan struct{})
			if m.seekPosition == m.result.duration() {
				m.seekPosition = 0
			}
			m.workers.Add(1)
			go func() {
				defer m.workers.Done()
				defer close(m.replayDone)
				m.replayErr = m.result.playFrom(replayCtx, m.seekPosition)
			}()
			return m, terminalTranslationPoll()
		}
		if m.finished && !m.saving && m.replayDone == nil && (key == "n" || (key == "enter" && !m.hasResult())) {
			if err := m.resetSession(); err != nil {
				m.lastErr, m.note = err, err.Error()
			}
			return m, nil
		}
		if key == "s" && m.finished && !m.saving && !m.saved && m.hasResult() {
			m.note = ""
			m.saving = true
			m.saveDone = make(chan struct{})
			m.workers.Add(1)
			go func() {
				defer m.workers.Done()
				defer close(m.saveDone)
				m.savePath, m.saveErr = saveTerminalTranslation(m.ctx, m.result, m.folder)
			}()
			return m, terminalTranslationPoll()
		}
	}
	return m, nil
}

func (m *terminalTranslationModel) resetSession() error {
	if err := m.result.Close(); err != nil {
		return err
	}
	m.started, m.finished, m.stopping, m.saved = false, false, false, false
	m.note, m.savedPath, m.lastErr = "", "", nil
	m.result = terminalTranslationResult{}
	m.seekPosition = 0
	m.state, m.latest = terminalTranslationUpdate{}, terminalTranslationUpdate{}
	m.retrySetup = false
	m.focusSetupAction()
	return nil
}

func (m *terminalTranslationModel) startTranslation() tea.Cmd {
	var err error
	opts := m.options
	switch m.form.source {
	case translationText:
		if strings.TrimSpace(m.form.text) == "" {
			return m.openField("Text")
		}
	case translationFile:
		if strings.TrimSpace(m.form.path) == "" {
			return m.openField("File")
		}
		opts.File, err = audiomedia.OpenCaptureFile(m.form.path)
	default:
		err = m.validateCapture(opts.Device)
	}
	if err == nil && m.form.source != translationText && opts.Listen {
		err = m.validatePlayback()
		if err != nil {
			err = audioFailure("Playback requires FFplay. Turn Playback off in More settings or install FFplay.", err)
		}
	}
	if err != nil {
		if opts.File != nil {
			opts.File.Close()
		}
		m.note, m.lastErr = err.Error(), err
		m.retrySetup = true
		m.focusSetupAction()
		return nil
	}
	m.retrySetup = false
	m.note, m.lastErr = "", nil
	m.started = true
	m.state = terminalTranslationUpdate{State: "Connecting"}
	if m.form.source == translationText {
		m.state.State = "Translating"
	}
	m.latest = m.state
	m.stop = make(chan struct{})
	m.done = make(chan struct{})
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancelRun = cancel
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		defer close(m.done)
		if opts.File != nil {
			defer opts.File.Close()
		}
		update := func(state terminalTranslationUpdate) { m.mu.Lock(); m.latest = state; m.mu.Unlock() }
		if m.form.source == translationText {
			result, state := runTerminalText(ctx, m.translateText, m.form.text, opts.Config.Language, m.form.textModel)
			m.result = result
			update(state)
		} else {
			m.result = runTerminalTranslation(ctx, m.stop, opts, update)
		}
	}()
	return terminalTranslationPoll()
}

func (m *terminalTranslationModel) stopCapture() {
	if !m.stopping {
		m.stopping = true
		close(m.stop)
		if m.form.source == translationText {
			m.cancelRun()
		}
		m.state.State = "stopping"
	}
}
func (m *terminalTranslationModel) hasResult() bool {
	return m.result.stage != nil && (m.result.AudioBytes > 0 || m.state.Original != "" || m.state.Translation != "")
}

// Reuse the speech workbench's inline painter, wrapping negotiation and cleanup.
// The native terminal session does not create an HTTP listener or browser page.
func runTerminalTranslationUI(parent context.Context, command *cli.Command, connect func(context.Context, audiolive.Config) (audiolive.Session, error), translateText terminalTextTranslator, input, output *os.File) (resultErr error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	width, height, err := term.GetSize(output.Fd())
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	terminal, err := cliterminal.NewOutput(ctx, executable, output)
	if err != nil {
		return audioFailure("Could not prepare terminal output.", err)
	}
	defer func() { resultErr = errors.Join(resultErr, terminal.Close()) }()
	m := newTerminalTranslationModel(ctx, cancel, command, connect, translateText)
	defer func() {
		if m.savedPath != "" {
			_, writeErr := terminal.WriteContext(parent, []byte("Saved translation: "+readable.Text(m.savedPath)+"\n"))
			resultErr = errors.Join(resultErr, writeErr)
		}
	}()
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
	console := uv.NewConsole(input, output, os.Environ())
	if _, err = console.MakeRaw(); err != nil {
		return err
	}
	cursorOutput := &audioCursorOutput{output: terminal}
	painter := cliterminal.NewPainter(cursorOutput, cancel)
	defer painter.Stop()
	m.width, m.height = width, height
	m.dark = cliterminal.DarkBackground(os.Getenv("COLORFGBG"))
	profile := cliterminal.ColorProfile(output, os.Environ(), term.IsTerminal(output.Fd()))
	m.color = profile >= colorprofile.ANSI
	inline := &audioWorkbenchInline{model: m, painter: painter, output: output, profile: profile, cancel: cancel, cursorOutput: cursorOutput}
	program := tea.NewProgram(inline, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(io.Discard), tea.WithoutSignalHandler(), tea.WithoutRenderer(), tea.WithWindowSize(width, height), tea.WithColorProfile(profile))
	listenerDone := make(chan struct{})
	go func() {
		defer close(listenerDone)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w, h, sizeErr := term.GetSize(output.Fd())
				if sizeErr != nil {
					program.Send(audioWorkbenchSizeError{})
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
	contextErr := clioutput.DownloadContextError(ctx)
	cancel()
	m.workers.Wait()
	<-listenerDone
	writeErr := painter.Stop()
	cleanupErr := errors.Join(cliterminal.CloseFrame(terminal, inline.close(), m.exitCode != 0 || contextErr != nil), m.result.Close())
	resultErr = errors.Join(m.lastErr, runErr, contextErr, audioFailureIf("Could not finish the translation terminal session.", errors.Join(writeErr, inline.err, cleanupErr)))
	if m.exitCode != 0 {
		resultErr = errors.Join(cli.Exit("", m.exitCode), resultErr)
	}
	return resultErr
}
