package output

import (
	"errors"

	"github.com/openai/openai-cli/internal/jsonview"
	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/openai/openai-cli/internal/readable"
)

// writeReadableStream keeps API projection separate from incremental rendering.
// Neither stage consumes ahead, buffers a response, or opens a pager.
func writeReadableStream(iter jsonview.Iterator[JSON], opts ReadableOptions) error {
	out := Writer{Context: opts.Context, Sink: opts.Stdout}
	writer := readable.NewStreamWriter(out)
	route := opts.Route
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
