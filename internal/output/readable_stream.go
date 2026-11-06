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
	var agents transformers.AgentsStreamProjector
	for iter.Next() {
		value := iter.Current().Result
		event, projected, err := agents.Project(opts.Context, value, route)
		if err != nil {
			return errors.Join(err, iter.Err())
		}
		if !projected {
			event, projected = transformers.ProjectTextStream(value, route)
		}
		if !projected {
			event, projected = transformers.ProjectAudioStream(value, route)
		}
		if projected {
			err = writer.Write(event)
		} else {
			err = writer.WriteValue(value)
		}
		if err != nil {
			return errors.Join(err, iter.Err())
		}
	}
	if err := errors.Join(iter.Err(), writer.Finish(), writeAgentsStreamHint(opts, agents.HasOmissions())); err != nil {
		return err
	}
	if !writer.HasOutput() && !agents.HasOmissions() {
		return readable.WriteText(out, "No results.")
	}
	return opts.Context.Err()
}

// The shared diagnostic policy controls suppression and preserves write errors.
// Emit the omission hint once, after the readable stream finishes.
func writeAgentsStreamHint(opts ReadableOptions, omitted bool) error {
	if !omitted || !transformers.IsAgentsStream(opts.Route) || ResolveFormat(opts.FormatOptions) != "text" ||
		opts.Transform != "" || opts.RawOutput {
		return nil
	}
	return writeReadableHint(opts, "Some fields omitted. Use --format jsonl for complete events.")
}
