package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/openai/openai-cli/internal/updatenotice"
	"github.com/urfave/cli/v3"
)

// This optional footer runs only after successful bare interactive startup.
// Explicit help, commands, and completion never print a notice.
func showUpdateNotice(ctx context.Context, argv []string, root *cli.Command, check func(context.Context, string) string) {
	if ctx.Err() != nil || !updateNoticeEnvironment(argv, os.Getenv) {
		return
	}
	if !interactiveUpdateTerminals(root) {
		return
	}
	if message := check(ctx, root.Version); message != "" && ctx.Err() == nil {
		// An advisory failure must not change the successful help exit status.
		// Plain text avoids styling, cursor movement, and terminal queries.
		_, _ = fmt.Fprintln(os.Stderr, message)
	}
}

func updateNoticeEnvironment(argv []string, getenv func(string) string) bool {
	return len(argv) == 1 && argv[0] != "" && interactiveUpdateEnvironment(getenv)
}

func interactiveUpdateEnvironment(getenv func(string) string) bool {
	if value := strings.ToLower(getenv("OPENAI_CLI_NO_UPDATE_CHECK")); value != "" && value != "0" && value != "false" {
		return false
	}
	return terminal.AutomaticPresentation("auto", true, getenv)
}

func interactiveUpdateTerminals(root *cli.Command) bool {
	input, output := root.Reader, root.Writer
	if input == nil {
		input = os.Stdin
	}
	if output == nil {
		output = os.Stdout
	}
	// The generated command buffers ErrWriter for error formatting.
	// Startup diagnostics use the actual stderr destination, like app.Run.
	return updateNoticeTerminals(input, output, os.Stderr)
}

func updateNoticeTerminals(input io.Reader, output, diagnostic io.Writer) bool {
	file, ok := input.(*os.File)
	return ok && term.IsTerminal(file.Fd()) && terminal.IsWriterTerminal(output) && terminal.IsWriterTerminal(diagnostic)
}

// Record only the direct, default text response. List viewers, file saves,
// streaming, local commands, and explicit output modes use other render paths.
func recordDefaultAPIResponse(opts ShowJSONOpts) {
	if opts.Operation == "" || opts.OutputKind != OutputResponse || opts.ExplicitFormat ||
		opts.Transform != "" || opts.RawOutput || opts.CallerHandlesSignals ||
		opts.Stdout != os.Stdout || opts.Stderr != os.Stderr {
		return
	}
	invocation, _ := opts.Context.Value(outputInvocationKey{}).(*outputInvocation)
	if invocation == nil || invocation.command == nil {
		return
	}
	command := invocation.command
	root := command.Root()
	if commandAllowsOutputDiagnostics(command) && !root.IsSet("transform") && !root.IsSet("raw-output") &&
		!root.IsSet("format-error") && !root.IsSet("transform-error") && !command.IsSet("output") {
		invocation.defaultAPIResponse = true
	}
}

func updateAfterAPIResponse(ctx context.Context, root *cli.Command) {
	if ctx.Err() == nil && interactiveUpdateEnvironment(os.Getenv) && interactiveUpdateTerminals(root) {
		updatenotice.UpdateSilently(ctx, root.Version)
	}
}
