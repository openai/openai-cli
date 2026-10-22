package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/urfave/cli/v3"
)

// This optional footer runs only after successful bare interactive startup.
// Explicit help, commands, completion, and redirected streams never check or
// write cache state. Color overrides cannot opt scripts into this behavior.
func showUpdateNotice(ctx context.Context, argv []string, root *cli.Command, check func(context.Context, string) string) {
	if ctx.Err() != nil || !updateNoticeEnvironment(argv, os.Getenv) {
		return
	}
	input, output := root.Reader, root.Writer
	if input == nil {
		input = os.Stdin
	}
	if output == nil {
		output = os.Stdout
	}
	// The generated command buffers ErrWriter for error formatting.
	// Startup diagnostics use the actual stderr destination, like app.Run.
	diagnostic := os.Stderr
	if !updateNoticeTerminals(input, output, diagnostic) {
		return
	}
	if message := check(ctx, root.Version); message != "" && ctx.Err() == nil {
		// An advisory failure must not change the successful help exit status.
		// Plain text avoids styling, cursor movement, and terminal queries.
		_, _ = fmt.Fprintln(diagnostic, message)
	}
}

func updateNoticeEnvironment(argv []string, getenv func(string) string) bool {
	if len(argv) != 1 || argv[0] == "" {
		return false
	}
	if value := strings.ToLower(getenv("OPENAI_CLI_NO_UPDATE_CHECK")); value != "" && value != "0" && value != "false" {
		return false
	}
	return terminal.AutomaticPresentation("auto", true, getenv)
}

func updateNoticeTerminals(input io.Reader, output, diagnostic io.Writer) bool {
	file, ok := input.(*os.File)
	return ok && term.IsTerminal(file.Fd()) && terminal.IsWriterTerminal(output) && terminal.IsWriterTerminal(diagnostic)
}
