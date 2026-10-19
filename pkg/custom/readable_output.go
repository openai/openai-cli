package custom

import (
	"context"
	"strings"

	clioutput "github.com/openai/openai-cli/internal/output"
	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/urfave/cli/v3"
)

func resolvedOutputFormat(opts ShowJSONOpts) string {
	return clioutput.ResolveFormat(clioutput.FormatOptions{Format: opts.Format, Transform: opts.Transform, RawOutput: opts.RawOutput})
}

func configureReadableOutput(root *cli.Command) {
	for _, flag := range root.Flags {
		value, ok := flag.(*cli.StringFlag)
		if !ok || value.Name != "format" {
			continue
		}
		value.Usage = "Readable text by default, including pipes. Use json for full API data. Formats: " + strings.Join(OutputFormats, ", ")
		previous := value.Action
		value.Action = func(ctx context.Context, command *cli.Command, format string) error {
			if previous != nil {
				if err := previous(ctx, command, format); err != nil {
					return err
				}
			}
			// Generated list handlers choose the raw page-envelope path before
			// reaching the renderer. Normalize here so RAW and raw agree.
			if normalized := strings.ToLower(format); normalized != format {
				return command.Set("format", normalized)
			}
			return nil
		}
	}
}

// Assembly supplies destination classification and the current diagnostic policy.
func readableOutputOptions(opts ShowJSONOpts) clioutput.ReadableOptions {
	return clioutput.ReadableOptions{
		Context:            opts.Context,
		Route:              transformers.Route{Operation: opts.Operation, OutputKind: opts.OutputKind},
		FormatOptions:      clioutput.FormatOptions{Format: opts.Format, RawOutput: opts.RawOutput, Title: opts.Title, Transform: opts.Transform},
		Stdout:             opts.Stdout,
		Stderr:             opts.Stderr,
		Terminal:           isTerminal(opts.Stdout),
		DiagnosticsAllowed: outputDiagnosticsAllowed(opts.Context),
	}
}
