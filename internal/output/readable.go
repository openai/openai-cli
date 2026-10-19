package output

import (
	"context"
	"errors"
	"io"

	"github.com/openai/openai-cli/internal/jsonview"
	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
)

// ReadableOptions describes an already selected readable presentation. Assembly
// supplies a non-nil Context and writers, the final destination classification,
// and permission to emit optional diagnostics. The iterator is already transformed.
type ReadableOptions struct {
	Context context.Context
	Route   transformers.Route
	FormatOptions
	Stdout, Stderr     io.Writer
	Terminal           bool
	DiagnosticsAllowed bool
}

const ResourceSummaryHint = "Full data: --format json."

// WriteReadableResource applies resource projections to readable results without field extraction.
// Report omissions so each response or list can explain them once.
func WriteReadableResource(out io.Writer, value gjson.Result, opts ReadableOptions) (bool, error) {
	omitted := false
	if opts.Transform == "" && !opts.RawOutput {
		summary, hidden, err := transformers.SummarizeResource(opts.Context, value, opts.Route)
		if err != nil {
			return false, err
		}
		if summary.Exists() {
			value, omitted = summary, hidden
		}
	}
	return omitted, readable.Write(out, value)
}

// WriteReadableIterator renders records as they arrive, keeping stream state separate from list records.
func WriteReadableIterator(iter jsonview.Iterator[JSON], opts ReadableOptions) error {
	if opts.Route.OutputKind == transformers.OutputStreamEvent && opts.Transform == "" && !opts.RawOutput {
		return writeReadableStream(iter, opts)
	}
	out := Writer{Context: opts.Context, Sink: opts.Stdout}
	emitted := false
	omitted := false
	for iter.Next() {
		value := ApplyJSONPath(iter.Current().Result, opts.Transform)
		if opts.RawOutput && value.Type == gjson.String {
			// Keep raw strings byte-for-byte in pipes and escape terminal
			// controls using the same destination-aware formatter as ShowJSON.
			formatted, err := FormatJSON(iter.Current().Result, opts.FormatOptions, opts.Stdout, opts.Terminal)
			if err != nil {
				return errors.Join(err, iter.Err())
			}
			if _, err := out.Write(formatted); err != nil {
				return errors.Join(err, iter.Err())
			}
			emitted = true
			continue
		}
		if emitted {
			if _, err := io.WriteString(out, "\n"); err != nil {
				return errors.Join(err, iter.Err())
			}
		}
		hidden, err := WriteReadableResource(out, value, opts)
		if err != nil {
			return errors.Join(err, iter.Err())
		}
		omitted = omitted || hidden
		emitted = true
	}
	if omitted {
		if err := writeReadableHint(opts, ResourceSummaryHint); err != nil {
			return errors.Join(err, iter.Err())
		}
	}
	if err := iter.Err(); err != nil {
		return err
	}
	if !emitted {
		return readable.WriteText(out, "No results.")
	}
	return opts.Context.Err()
}

// Keep optional prose on the diagnostic channel with its original error origin.
func writeReadableHint(opts ReadableOptions, message string) error {
	if !opts.DiagnosticsAllowed {
		return nil
	}
	if err := readable.WriteText(Writer{Context: opts.Context, Sink: opts.Stderr}, message); err != nil {
		return &DiagnosticError{Err: err}
	}
	return nil
}
