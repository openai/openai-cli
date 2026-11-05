// Package commandsuggest records commands explicitly authored by CLI presenters.
// It never discovers commands by inspecting arbitrary output.
package commandsuggest

import (
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/term"
	"github.com/urfave/cli/v3"
)

const (
	metadataKey     = "openai-command-suggestions"
	maxCommands     = 128
	maxCommandBytes = 16 * 1024
	maxCaptureBytes = 2 * 1024 * 1024
)

// Recorder receives explicit suggestions and their human-readable destination.
// An empty shell means that the suggestion applies to any supported shell.
type Recorder interface {
	Record(output io.Writer, shell, text string)
}

// SetRecorder attaches a recorder without replacing the command's output writer.
func SetRecorder(command *cli.Command, recorder Recorder) {
	root := command.Root()
	if root.Metadata == nil {
		root.Metadata = make(map[string]interface{})
	}
	root.Metadata[metadataKey] = recorder
}

// Configure enables the optional shell transport and returns its idempotent flush.
// The shell supplies an already-open private file, never a pathname to create.
func Configure(root *cli.Command) func() {
	shell := os.Getenv("OPENAI_CLI_COMMANDS_SHELL")
	switch shell {
	case "bash", "zsh", "fish":
	default:
		return func() {}
	}
	fd, err := strconv.Atoi(os.Getenv("OPENAI_CLI_COMMANDS_FD"))
	if err != nil || fd < 3 || fd > 1024 {
		return func() {}
	}
	sink := privateSink(fd)
	if sink == nil {
		return func() {}
	}
	capture := &collector{shell: shell, sink: sink, terminal: isTerminal, seen: make(map[string]struct{})}
	if shell == "zsh" {
		capture.clickPrefix = clickPrefix(os.Getenv("OPENAI_CLI_COMMANDS_CLICK_SESSION"), os.Getenv("OPENAI_CLI_COMMANDS_CLICK_GENERATION"))
	}
	SetRecorder(root, capture)
	return capture.flush
}

// Record registers a command and adds an optional terminal insertion link.
func Record(command *cli.Command, text string) string {
	return RecordFor(command, "", text)
}

// RecordFor records a command only for the named shell integration.
func RecordFor(command *cli.Command, shell, text string) string {
	if command == nil {
		return text
	}
	output := command.Root().Writer
	if output == nil {
		output = os.Stdout
	}
	return record(command, output, shell, text)
}

// RecordTo registers a command at its actual human-readable output destination.
func RecordTo(command *cli.Command, output io.Writer, text string) string {
	return record(command, output, "", text)
}

// RecordLabel displays a label whose insertion target is an explicit command.
// Registration occurs when the presenter renders the label, never during setup.
func RecordLabel(command *cli.Command, label, target string) string {
	if command == nil || !validCommand(label) || !validCommand(target) {
		return label
	}
	recorder, ok := command.Root().Metadata[metadataKey].(Recorder)
	if !ok || recorder == nil {
		return label
	}
	output := command.Root().Writer
	if output == nil {
		output = os.Stdout
	}
	if renderer, ok := recorder.(interface {
		RenderLabel(io.Writer, string, string, string) string
	}); ok {
		return renderer.RenderLabel(output, "", label, target)
	}
	recorder.Record(output, "", target)
	return label
}

func record(command *cli.Command, output io.Writer, shell, text string) string {
	if command != nil {
		if recorder, ok := command.Root().Metadata[metadataKey].(Recorder); ok && recorder != nil {
			if renderer, ok := recorder.(interface {
				Render(io.Writer, string, string) string
			}); ok {
				return renderer.Render(output, shell, text)
			}
			recorder.Record(output, shell, text)
		}
	}
	return text
}

type collector struct {
	mu          sync.Mutex
	shell       string
	sink        io.WriteCloser
	terminal    func(io.Writer) bool
	seen        map[string]struct{}
	commands    []string
	bytes       int
	closed      bool
	failed      bool
	clickPrefix string
}

func (c *collector) Record(output io.Writer, shell, text string) {
	c.Render(output, shell, text)
}

func (c *collector) Render(output io.Writer, shell, text string) string {
	return c.RenderLabel(output, shell, text, text)
}

func (c *collector) RenderLabel(output io.Writer, shell, label, target string) string {
	if shell != "" && shell != c.shell || !validCommand(label) || !validCommand(target) || !c.terminal(output) {
		return label
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.renderLabelLocked(label, target)
}

func (c *collector) renderLocked(text string) string {
	return c.renderLabelLocked(text, text)
}

func (c *collector) renderLabelLocked(label, target string) string {
	if c.closed || c.failed {
		return label
	}
	if _, exists := c.seen[target]; !exists {
		if len(c.commands) >= maxCommands || c.bytes+len(target)+1 > maxCaptureBytes {
			return label
		}
		c.seen[target] = struct{}{}
		c.commands = append(c.commands, target)
		c.bytes += len(target) + 1
	}
	if c.clickPrefix != "" {
		for index, command := range c.commands {
			if command == target {
				return "\x1b]8;;" + c.clickPrefix + strconv.Itoa(index+1) + "\x1b\\" + label + "\x1b]8;;\x1b\\"
			}
		}
	}
	return label
}

func (c *collector) flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	// This optional transport must never change command output or exit status.
	if !c.failed {
		_, _ = io.WriteString(c.sink, strings.Join(c.commands, "\n")+trailingNewline(c.commands))
	}
	_ = c.sink.Close()
}

func trailingNewline(commands []string) string {
	if len(commands) == 0 {
		return ""
	}
	return "\n"
}

func validCommand(text string) bool {
	if strings.TrimSpace(text) == "" || len(text) > maxCommandBytes || !utf8.ValidString(text) {
		return false
	}
	for _, r := range text {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' {
			return false
		}
	}
	return true
}

func isTerminal(output io.Writer) bool {
	file, ok := output.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(file.Fd())
}
