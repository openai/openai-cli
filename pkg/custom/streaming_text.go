package custom

import (
	"errors"

	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/pkg/transformers"
)

// showReadableStream keeps API projection separate from incremental rendering.
// Neither stage consumes ahead, buffers a response, or opens a pager.
func showReadableStream(iter jsonview.Iterator[outputJSON], opts ShowJSONOpts) error {
	out := outputWriter{ctx: opts.Context, out: opts.Stdout}
	writer := readable.NewStreamWriter(out)
	route := transformers.Route{Operation: opts.Operation, OutputKind: opts.OutputKind}
	for iter.Next() {
		value := iter.Current().Result
		event, projected := transformers.ProjectTextStream(value, route)
		if !projected {
			event, projected = transformers.ProjectAudioStream(value, route)
		}
		var err error
		if projected {
			err = writer.Write(event)
		} else {
			err = writer.WriteValue(value)
		}
		if err != nil {
			return errors.Join(err, iter.Err())
		}
	}
	if err := errors.Join(iter.Err(), writer.Finish()); err != nil {
		return err
	}
	if !writer.HasOutput() {
		return readable.WriteText(out, "No results.")
	}
	return opts.Context.Err()
}
