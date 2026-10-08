package custom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/urfave/cli/v3"
)

type outputPolicyKey struct{}

type outputPolicy struct {
	quiet       bool
	diagnostics bool
}

// Diagnostic failures belong to stderr, even inside stdout's stream lifecycle.
type diagnosticWriteError struct{ error }

func (e *diagnosticWriteError) Unwrap() error { return e.error }

func quietOutput(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	policy, _ := ctx.Value(outputPolicyKey{}).(outputPolicy)
	return policy.quiet
}

// Optional prose must never precede a selected machine-readable error payload.
func outputDiagnosticsAllowed(ctx context.Context) bool {
	if ctx == nil {
		return true
	}
	policy, ok := ctx.Value(outputPolicyKey{}).(outputPolicy)
	return !ok || policy.diagnostics
}

func outputPolicyContext(ctx context.Context, command *cli.Command) context.Context {
	root := command.Root()
	return context.WithValue(ctx, outputPolicyKey{}, outputPolicy{
		quiet: root.Bool("quiet"),
		diagnostics: !root.Bool("quiet") && errorOutputFormat(root) == "text" &&
			root.String("transform-error") == "",
	})
}

func writeOutputHint(opts ShowJSONOpts, message string) error {
	if !outputDiagnosticsAllowed(opts.Context) {
		return nil
	}
	if err := readable.WriteText(outputWriter{ctx: opts.Context, out: opts.Stderr}, message); err != nil {
		return &diagnosticWriteError{err}
	}
	return nil
}

func writeOutputReceipt(ctx context.Context, message string) error {
	if !outputDiagnosticsAllowed(ctx) {
		return nil
	}
	return readable.WriteText(outputWriter{ctx: ctx, out: os.Stderr}, message)
}

func configureOutputPolicy(root *cli.Command) {
	root.Flags = append(root.Flags,
		&cli.BoolFlag{Name: "quiet", Usage: "Suppress optional feedback. Keep data, help, errors and exit status. Overrides verbose.", HideDefault: true},
		&cli.BoolFlag{Name: "verbose", Usage: "Report command details on stderr. Omitted for machine errors; use --format-error text to enable.", HideDefault: true},
	)
	var visit func(*cli.Command, []string)
	visit = func(command *cli.Command, path []string) {
		if command.Name == "__complete" || command.Name == "help" {
			return
		}
		if command != root {
			path = append(slices.Clone(path), command.Name)
		}
		if command.Action != nil {
			next := command.Action
			// Capture declaration names, never the invoked executable or arguments.
			label := strings.Join(path, " ")
			protocol := command.Name == "@completion"
			command.Action = func(ctx context.Context, command *cli.Command) error {
				root := command.Root()
				ctx = outputPolicyContext(ctx, command)
				err := next(ctx, command)
				if protocol && !command.Bool("install-picker") && !command.Bool("uninstall-picker") ||
					!root.Bool("verbose") || !outputDiagnosticsAllowed(ctx) {
					return err
				}
				// Optional feedback must not restart output after an interrupted action.
				var exit cli.ExitCoder
				if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
					errors.As(err, &exit) && (exit.ExitCode() == 124 || exit.ExitCode() == 130 || exit.ExitCode() == 143) {
					return err
				}
				format := strings.ToLower(root.String("format"))
				if format == "" {
					format = "auto"
				}
				if !slices.Contains(OutputFormats, format) {
					format = "unknown"
				}
				return errors.Join(err, writeVerboseResult(os.Stderr, label, format, err))
			}
		}
		for _, child := range command.Commands {
			visit(child, path)
		}
	}
	visit(root, nil)
}

// Only static command declarations and validated format names reach this sink.
// An action returning nil confirms command completion, not asynchronous API work.
func writeVerboseResult(out io.Writer, command, format string, failure error) error {
	result := "completed"
	if failure != nil {
		result = "failed"
	}
	_, err := fmt.Fprintf(outputWriter{ctx: context.Background(), out: out},
		"Command: %s\nFormat option: %s\nCommand result: %s\n", command, format, result)
	return err
}
