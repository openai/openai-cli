package audio

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/openai/openai-cli/internal/audiomedia"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/urfave/cli/v3"
)

const (
	audioSettingHistory = 5
	audioSettingMore    = 6
)

type audioWorkResult struct {
	entry      audiohistory.Entry
	analysis   audiomedia.Analysis
	measured   bool
	position   time.Duration
	finished   bool
	savedPaths []string
}
type audioWorkMessage struct {
	kind          string
	result        audioWorkResult
	entries       []audiohistory.Entry
	err           error
	playID        uint64
	path, entryID string
}
type audioWorkTick time.Time

const (
	audioPlayingNote      = "Playing. Space or X stops."
	audioActionFailedNote = "Audio action failed."
)

// Closing prevents late Bubble Tea commands from starting work after teardown.
type audioWorkers struct {
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

func (w *audioWorkers) command(ctx context.Context, run func(context.Context) audioWorkMessage) tea.Cmd {
	return func() tea.Msg {
		w.mu.Lock()
		if w.closed {
			w.mu.Unlock()
			return nil
		}
		w.wg.Add(1)
		w.mu.Unlock()
		defer w.wg.Done()
		return run(ctx)
	}
}
func (w *audioWorkers) close(cancel context.CancelFunc) {
	w.mu.Lock()
	w.closed = true
	cancel()
	w.mu.Unlock()
	w.wg.Wait()
}

type audioWorkbench struct {
	ctx                        context.Context
	command                    *cli.Command
	next                       cli.ActionFunc
	store                      audiohistory.Store
	workers                    *audioWorkers
	settings                   audiohistory.Settings
	input, instructions, path  textarea.Model
	speedInput, saveFolder     textarea.Model
	focus, setting, choice     int
	modal                      string
	choices                    []string
	entries                    []audiohistory.Entry
	result                     *audioWorkResult
	priorResult                *audioWorkResult
	invocation                 string
	width, height              int
	color, dark                bool
	busy, quit, playing        bool
	note                       string
	failure                    audioFeedback
	failureNote, failureParent string
	failureOffset              int
	moreChoice, recoveryChoice int
	resultAction               int
	waveformFocused            bool
	recoveryParent             string
	infoTitle, infoText        string
	commandToPrint             string
	infoParent                 string
	infoOffset                 int
	recoveryInfo               *audioInformationState
	lastErr                    error
	exitCode                   int
	lastFinished               time.Time
	started                    time.Time
	playID                     uint64
	playCancel                 context.CancelFunc
	playDone                   <-chan struct{}
	playProgress               *audioPlaybackProgress
	saveFocus                  int
}

func newAudioWorkbench(ctx context.Context, command *cli.Command, next cli.ActionFunc, store audiohistory.Store) *audioWorkbench {
	newEditor := func(placeholder string) textarea.Model {
		t := textarea.New()
		t.CharLimit = 0
		// Terminal paste remains supported. Disable the widget's unbounded native
		// clipboard subprocesses; the workbench owns all launched helpers.
		t.KeyMap.Paste.SetEnabled(false)
		t.KeyMap.CopySelection.SetEnabled(false)
		t.ShowLineNumbers = false
		t.Prompt = ""
		t.Placeholder = placeholder
		t.SetWidth(60)
		t.SetHeight(2)
		return t
	}
	m := &audioWorkbench{ctx: ctx, command: command, next: next, store: store, workers: &audioWorkers{}, settings: audioDefaultSettings(), color: os.Getenv("NO_COLOR") == "", dark: true,
		input: newEditor("Enter text to speak"), instructions: newEditor("Add speaking instructions (optional)"), path: newEditor("Filename for the original audio file"), saveFolder: newEditor("Folder for saved audio"), speedInput: newEditor("1.00")}
	m.instructions.SetValue(audioInstructionsExample)
	m.invocation, _ = command.Root().Metadata["help-invocation"].(string)
	if m.invocation == "" {
		m.invocation = "openai"
	}
	if pkg, ok := strings.CutPrefix(m.invocation, "go run "); ok {
		if directory, err := os.Getwd(); err == nil {
			m.invocation = "go -C " + audioShellQuote(directory) + " run " + audioShellQuote(pkg)
		}
	}
	m.input.Focus()
	return m
}

func (m *audioWorkbench) Init() tea.Cmd { return tea.RequestBackgroundColor }
func audioTick() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(t time.Time) tea.Msg { return audioWorkTick(t) })
}
func (m *audioWorkbench) usable() bool { return m.width >= 40 && m.height >= 12 }

func (m *audioWorkbench) resizeEditors() {
	for _, editor := range []*textarea.Model{&m.input, &m.instructions, &m.path, &m.saveFolder, &m.speedInput} {
		width := max(1, m.audioWidth()-4)
		if (editor == &m.path || editor == &m.saveFolder) && m.modal == "save" {
			labelWidth := 14
			if m.height < 24 || m.width < 60 {
				labelWidth = 8
			}
			width = max(1, m.audioWidth()-labelWidth-3)
		}
		if editor == &m.instructions && m.height >= 24 && m.width >= 60 {
			width = max(1, m.audioWidth()-17)
		}
		if editor.Width() != width {
			editor.SetWidth(width)
		}
		height := 1
		if (editor == &m.path || editor == &m.saveFolder) && m.modal == "save" {
			// Reserve the other control rows, feedback, and the terminal's spare row.
			reserved := 5 + len(m.feedbackLines())
			if m.height >= 24 && m.width >= 60 {
				reserved += 2
			}
			height = min(4, max(1, editor.LineInfo().Height), max(1, m.height-1-reserved))
		}
		if m.height >= 24 && (editor == &m.input || editor == &m.instructions) {
			height = min(3, editor.LineCount())
			if m.width >= 60 {
				height = audioEditorHeight(*editor)
			}
		}
		if editor.Height() != height {
			// Dynamic sizing clamps the viewport against actual content rows.
			// Keep the temporary display bounds out of input processing; Bubbles
			// also uses MaxHeight as a legacy input limit.
			dynamic, minimum, maximum := editor.DynamicHeight, editor.MinHeight, editor.MaxHeight
			editor.DynamicHeight, editor.MinHeight, editor.MaxHeight = true, height, height
			editor.SetWidth(editor.Width())
			editor.DynamicHeight, editor.MinHeight, editor.MaxHeight = dynamic, minimum, maximum
		}
	}
}

// Measure wrapped rows without moving the editor's shared viewport.
func audioEditorHeight(editor textarea.Model) int {
	if editor.LineCount() >= 3 {
		return 3
	}
	if editor.LineCount() == 1 {
		return min(3, max(1, editor.LineInfo().Height))
	}
	// A textarea value copy still shares its viewport. Use a fresh measuring
	// editor for two logical lines so cursor movement cannot scroll the form.
	probe := textarea.New()
	probe.Prompt, probe.ShowLineNumbers = "", false
	probe.CharLimit, probe.MaxHeight, probe.MaxContentHeight, probe.MaxWidth = 0, 0, 0, 0
	probe.SetStyles(textarea.Styles{})
	probe.SetWidth(editor.Width())
	height := 0
	for _, line := range strings.Split(editor.Value(), "\n") {
		probe.SetValue(line)
		height += max(1, probe.LineInfo().Height)
		if height >= 3 {
			return 3
		}
	}
	return height
}

func (m *audioWorkbench) Update(message tea.Msg) (model tea.Model, command tea.Cmd) {
	defer m.resizeEditors()
	defer func() { command = tea.Batch(command, m.reconcileRecovery()) }()
	if m.quit {
		return m, nil
	}
	if paste, ok := message.(tea.PasteMsg); ok {
		paste.Content = audioEditorText(paste.Content)
		message = paste
	}
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
	case audioWorkTick:
		m.capturePlaybackProgress()
		if m.busy || m.playing {
			return m, audioTick()
		}
	case audioWorkMessage:
		if msg.kind == "play" {
			if msg.playID != m.playID {
				return m, nil
			}
			m.capturePlaybackProgress()
			if m.result != nil {
				m.result.finished = msg.err == nil
			}
			m.playProgress = nil
			if m.playCancel != nil {
				m.playCancel()
				m.playCancel = nil
			}
			m.playing = false
			if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
				m.lastErr = msg.err
				m.note = "Playback failed. FFplay is required; compressed audio also needs FFmpeg."
				m.rememberFailure("play", msg.err)
			} else if m.note == audioPlayingNote {
				m.note = "Finished."
				if msg.err != nil {
					m.note = "Stopped."
				}
			}
			return m, nil
		}
		m.busy = false
		m.lastFinished = time.Now()
		if msg.err != nil {
			m.lastErr = msg.err
			m.note = audioActionFailedNote
		}
		if msg.result.entry.ID != "" {
			if msg.result.entry.Settings.Preview {
				if m.result != nil && !m.result.entry.Settings.Preview {
					m.priorResult = m.result
				}
			} else {
				m.priorResult = nil
			}
			m.result = &msg.result
			m.resultAction = 0
			m.waveformFocused = false
			m.focusSection(4)
			if msg.kind == "open" {
				m.settings = msg.result.entry.Settings
				m.input.SetValue(audioEditorText(m.settings.Input))
				m.instructions.SetValue(audioEditorText(m.settings.Instructions))
			}
			if msg.err == nil {
				m.lastErr = nil
				m.note = "Audio retained. Space plays or stops. S saves the original bytes."
			}
			if msg.result.entry.Settings.Preview && msg.err == nil {
				return m, m.togglePlayback()
			}
		}
		if msg.kind == "history" {
			m.entries = msg.entries
			m.modal = "history"
			m.choice = 0
			m.note = ""
			if msg.err != nil {
				m.note = "Some audio history could not be read."
			}
		}
		if msg.kind == "export" && msg.err == nil {
			m.note = "Saved original audio bytes."
			if m.result != nil && m.result.entry.ID == msg.entryID {
				m.result.savedPaths = []string{msg.path}
			}
			m.lastErr = nil
		}
		if msg.err != nil {
			m.rememberFailure(msg.kind, msg.err)
		}
	case tea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+c" {
			m.quit = true
			m.exitCode = 130
			m.stopPlayback()
			return m, tea.Quit
		}
		if !m.usable() {
			return m, nil
		}
		if key == "esc" {
			if m.modal == "recovery" {
				return m, m.leaveRecovery()
			}
			if m.modal == "info" {
				m.modal, m.infoParent = m.infoParent, ""
				m.lastFinished = time.Now()
				return m, m.restoreEditorFocus()
			}
			if m.modal == "error" {
				m.modal, m.failureParent = m.failureParent, ""
				m.failureOffset = 0
				m.lastFinished = time.Now()
				return m, m.restoreEditorFocus()
			}
			if m.modal != "" {
				m.modal = ""
				m.path.Blur()
				m.saveFolder.Blur()
				m.speedInput.Blur()
				m.lastFinished = time.Now()
				return m, nil
			}
			if m.busy {
				return m, nil
			}
			if m.focus == 4 {
				m.stopPlayback()
				if m.result != nil && m.result.entry.Settings.Preview {
					m.result = m.priorResult
					m.priorResult = nil
				}
				m.focusSection(0)
				return m, nil
			}
			m.quit = true
			m.stopPlayback()
			return m, tea.Quit
		}
		if m.busy {
			return m, nil
		}
		if key == "f1" && m.activeFailure() && m.modal != "error" {
			m.failureParent, m.modal, m.failureOffset = m.modal, "error", 0
			return m, nil
		}
		if m.modal != "" {
			return m, m.updateModal(message, key)
		}
		if handled, command := m.navigateSection(key); handled {
			return m, command
		}
		if key == "ctrl+o" && m.result != nil {
			return m, m.focusSection(4)
		}
		if key == "ctrl+g" {
			return m, m.generate(false)
		}
		if m.focus == 2 {
			switch key {
			case "left", "right":
				if m.setting == 3 {
					step := .25
					if key == "left" {
						step = -.25
					}
					m.settings.Speed = max(.25, min(4, m.settings.Speed+step))
				}
			case "enter":
				return m, m.activateSetting()
			case "p":
				if m.setting == 1 {
					return m, m.generate(true)
				}
			}
			return m, nil
		}
		if m.focus == 4 {
			switch key {
			case "left", "right", "shift+left", "shift+right":
				if m.waveformFocused {
					m.seekResult(key)
				}
			case "space", "enter":
				if time.Since(m.lastFinished) < 750*time.Millisecond {
					return m, nil
				}
				if key == "enter" && !m.waveformFocused && m.resultAction == 1 {
					return m, m.openSave()
				}
				return m, m.togglePlayback()
			case "s":
				return m, m.openSave()
			case "x":
				m.stopPlayback()
			case "h":
				return m, m.history()
			}
			return m, nil
		}
		if (m.focus == 0 || m.focus == 1) && key == "enter" {
			return m, m.generate(false)
		}
		if (m.focus == 0 || m.focus == 1) && key == "alt+enter" {
			if m.focus == 0 {
				m.input.InsertString("\n")
			} else {
				m.instructions.InsertString("\n")
			}
			return m, nil
		}
	}
	if !m.usable() || m.busy {
		return m, nil
	}
	if m.modal == "save" {
		return m, m.updateSaveEditor(message)
	}
	if m.modal == "speed" {
		var cmd tea.Cmd
		m.speedInput, cmd = m.speedInput.Update(message)
		return m, cmd
	}
	if m.modal == "" && m.focus == 0 {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(message)
		return m, cmd
	}
	if m.modal == "" && m.focus == 1 {
		var cmd tea.Cmd
		m.instructions, cmd = m.instructions.Update(message)
		return m, cmd
	}
	return m, nil
}

func (m *audioWorkbench) activateSetting() tea.Cmd {
	switch m.setting {
	case 0:
		m.choices = audioModels
	case 1:
		m.choices = audioModelVoices(m.settings.Model)
	case 2:
		m.choices = audioFormats
	case 3:
		m.modal = "speed"
		m.speedInput.SetValue(strconv.FormatFloat(m.settings.Speed, 'f', -1, 64))
		return m.speedInput.Focus()
	case 4:
		return m.generate(false)
	case audioSettingHistory:
		return m.history()
	case audioSettingMore:
		m.modal, m.moreChoice = "more", 0
		return nil
	}
	m.modal = "choice"
	current := []string{m.settings.Model, m.settings.Voice, m.settings.Format}[m.setting]
	m.choice = max(0, slices.Index(m.choices, current))
	return nil
}

func (m *audioWorkbench) updateModal(message tea.Msg, key string) tea.Cmd {
	if m.modal == "more" {
		return m.updateMore(key)
	}
	if m.modal == "recovery" {
		return m.updateRecovery(key)
	}
	if m.modal == "info" {
		if m.infoTitle == audioCommandTitle && key == "ctrl+p" {
			if _, omit := requestconfig.NextCommandOptions(m.command.Root()); omit {
				return nil
			}
			m.commandToPrint = m.commandPreview()
			m.quit = true
			m.stopPlayback()
			return tea.Quit
		}
		m.infoOffset = audioScrollOffset(m.infoOffset, len(m.infoRows()), m.informationHeight(), key)
		return nil
	}
	if m.modal == "error" {
		limit := max(0, len(m.failureDetailRows())-m.failureDetailHeight())
		m.failureOffset = min(m.failureOffset, limit)
		switch key {
		case "up":
			m.failureOffset = max(0, m.failureOffset-1)
		case "down":
			m.failureOffset = min(limit, m.failureOffset+1)
		case "pgup":
			m.failureOffset = max(0, m.failureOffset-m.failureDetailHeight())
		case "pgdown":
			m.failureOffset = min(limit, m.failureOffset+m.failureDetailHeight())
		case "home":
			m.failureOffset = 0
		case "end":
			m.failureOffset = limit
		}
		return nil
	}
	if m.modal == "speed" {
		if key != "enter" {
			var cmd tea.Cmd
			m.speedInput, cmd = m.speedInput.Update(message)
			return cmd
		}
		speed, err := strconv.ParseFloat(strings.TrimSpace(m.speedInput.Value()), 64)
		if err != nil || math.IsNaN(speed) || math.IsInf(speed, 0) || speed < .25 || speed > 4 {
			m.note = "Speed must be a number from 0.25 to 4."
			return nil
		}
		m.settings.Speed = speed
		m.modal = ""
		m.speedInput.Blur()
		m.note = ""
		return nil
	}
	if m.modal == "save" {
		return m.updateSave(message, key)
	}
	count := len(m.choices)
	if m.modal == "history" {
		count = len(m.entries)
	}
	if count == 0 {
		return nil
	}
	switch key {
	case "up":
		m.choice = (m.choice + count - 1) % count
	case "down":
		m.choice = (m.choice + 1) % count
	case "enter":
		if m.modal == "history" {
			id := m.entries[m.choice].ID
			m.modal = ""
			m.stopPlayback()
			return m.work("open", func(ctx context.Context) audioWorkMessage { return m.load(ctx, id, "open") })
		}
		value := m.choices[m.choice]
		switch m.setting {
		case 0:
			m.settings.Model = value
		case 1:
			m.settings.Voice = value
		case 2:
			m.settings.Format = value
		}
		m.modal = ""
	case "p":
		if m.modal == "choice" && m.setting == 1 {
			m.settings.Voice = m.choices[m.choice]
			m.modal = ""
			return m.generate(true)
		}
	}
	return nil
}

func (m *audioWorkbench) work(kind string, run func(context.Context) audioWorkMessage) tea.Cmd {
	m.busy = true
	m.started = time.Now()
	m.note = "Working: " + kind
	return tea.Batch(m.workers.command(m.ctx, run), audioTick())
}

func (m *audioWorkbench) generate(preview bool) tea.Cmd {
	if time.Since(m.lastFinished) < 750*time.Millisecond {
		return nil
	}
	settings := m.effectiveSettings()
	settings.Preview = preview
	if preview {
		settings.Input = audioAuditionText
	}
	if err := validateAudioSettings(settings); err != nil {
		m.note = err.Error()
		m.failure = audioFailureFeedback(m.command, "generate", err)
		m.failureNote = m.note
		m.openRecovery()
		return nil
	}
	m.stopPlayback()
	return m.work("generate", func(ctx context.Context) audioWorkMessage {
		if preview {
			entries, listErr := m.store.List(ctx)
			if listErr != nil {
				return audioWorkMessage{kind: "generate", err: classifiedAudioFailure(audioHistoryFailure, "Could not inspect cached auditions. No speech request was sent.", listErr)}
			}
			for _, entry := range entries {
				if entry.Settings == settings {
					return m.load(ctx, entry.ID, "generate")
				}
			}
		}
		entry, err := generateAudio(ctx, m.command, m.next, m.store, settings)
		if err != nil {
			return audioWorkMessage{kind: "generate", result: audioWorkResult{entry: entry}, err: err}
		}
		return m.load(ctx, entry.ID, "generate")
	})
}

func (m *audioWorkbench) load(ctx context.Context, id, kind string) audioWorkMessage {
	message := audioWorkMessage{kind: kind}
	snapshot, err := m.store.Open(ctx, id)
	if err != nil {
		message.err = classifiedAudioFailure(audioHistoryFailure, "Could not reopen retained audio.", err)
		return message
	}
	message.result.entry = snapshot.Entry
	message.result.analysis, err = audiomedia.Analyze(ctx, snapshot.File, snapshot.Entry.Settings.Format)
	message.result.measured = err == nil
	if err := errors.Join(err, snapshot.Close()); err != nil {
		message.err = classifiedAudioFailure(audioMeasurementFailure, "Audio is retained, but measurement failed. Compressed formats need FFmpeg on PATH.", err)
	}
	return message
}

func (m *audioWorkbench) history() tea.Cmd {
	return m.work("history", func(ctx context.Context) audioWorkMessage {
		entries, err := m.store.List(ctx)
		visible := make([]audiohistory.Entry, 0, len(entries))
		for _, entry := range entries {
			if !entry.Settings.Preview {
				visible = append(visible, entry)
			}
		}
		return audioWorkMessage{kind: "history", entries: visible, err: audioFailureIf("Some audio history could not be read.", err)}
	})
}

func (m *audioWorkbench) stopPlayback() {
	m.capturePlaybackProgress()
	if m.playing && m.result != nil {
		m.result.finished = false
	}
	m.playProgress = nil
	if m.playCancel != nil {
		m.playCancel()
		m.playCancel = nil
	}
	m.playID++
	m.playing = false
	if m.note == audioPlayingNote {
		m.note = "Stopped."
	}
}

func (m *audioWorkbench) togglePlayback() tea.Cmd {
	if m.result == nil {
		return nil
	}
	if m.playing {
		m.stopPlayback()
		return nil
	}
	offset := m.result.playbackPosition()
	if m.result.finished {
		offset, m.result.position, m.result.finished = 0, 0, false
	}
	if duration := m.result.seekDuration(); duration > 0 && offset >= duration {
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.playCancel = cancel
	m.playID++
	id := m.playID
	entryID := m.result.entry.ID
	previous := m.playDone
	done := make(chan struct{})
	m.playDone = done
	m.playing = true
	progress := &audioPlaybackProgress{entryID: entryID}
	m.playProgress = progress
	m.note = audioPlayingNote
	return tea.Batch(m.workers.command(ctx, func(ctx context.Context) audioWorkMessage {
		defer close(done)
		message := audioWorkMessage{kind: "play", playID: id}
		if previous != nil {
			select {
			case <-previous:
			case <-ctx.Done():
				message.err = ctx.Err()
				return message
			}
		}
		snapshot, err := m.store.Open(ctx, entryID)
		if err == nil {
			err = errors.Join(audiomedia.PlayWithProgress(ctx, snapshot.File, snapshot.Entry.Settings.Format, offset, progress.record), snapshot.Close())
		}
		message.err = audioFailureIf("Could not play the retained audio.", err)
		return message
	}), audioTick())
}

func audioDuration(seconds float64) string {
	ms := int64(seconds * 1000)
	return fmt.Sprintf("%02d:%02d.%03d", ms/60000, ms/1000%60, ms%1000)
}

// Textarea maps CR and LF separately. Normalize a pasted Windows line ending
// once so it cannot become an extra blank line in the generated speech text.
func audioEditorText(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
}
