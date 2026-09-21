package transformers

import (
	"context"

	"github.com/tidwall/gjson"
)

// Projection contains optional readable views of an API value. Text carries
// stream identity and completion metadata; Summary contains selected resource
// fields. Neither replaces the underlying JSON or includes terminal escaping.
type Projection struct {
	Text    ReadableValue
	Summary gjson.Result
	Omitted bool // the summary left out fields; the presenter owns any explanation
}

// Output keeps a transformed value separate from its readable views. A renderer
// must explicitly choose a projection; machine output always uses Value.
type Output struct {
	Value      gjson.Result
	Projection Projection
}

// Pipeline prepares one value without performing I/O or consuming an iterator.
// A zero pipeline is identity with no readable projection, suitable for an
// explicit API-data mode. SelectPipeline supplies the default behavior.
type Pipeline struct {
	Transform Transformer
	Project   func(gjson.Result) Projection
}

// SelectPipeline is the single registry for route-specific output behavior.
// The custom output boundary selects once, after deciding whether the user's
// requested format needs default presentation or untouched API data.
func SelectPipeline(route Route) Pipeline {
	pipeline := Pipeline{Transform: Identity}
	switch route.OutputKind {
	case OutputResponse, OutputPageItem, OutputStreamEvent:
	default:
		return pipeline
	}
	if IsImageOperation(route.Operation) {
		switch route.OutputKind {
		case OutputResponse:
			pipeline.Transform = imageOutputOnly(ImageResponse)
		case OutputStreamEvent:
			pipeline.Transform = imageOutputOnly(ImageStreamEvent)
		}
	}
	pipeline.Project = func(value gjson.Result) Projection {
		projection := Projection{Text: Readable(value, route)}
		if !projection.Text.IsText && !projection.Text.Skip {
			projection.Summary, _, projection.Omitted = summarize(value, route)
		}
		return projection
	}
	return pipeline
}

// Prepare normalizes and projects a value once each. Cancellation is checked
// before work and between stages; partially prepared output is not published.
func (pipeline Pipeline) Prepare(ctx context.Context, value gjson.Result) (Output, error) {
	if err := ctx.Err(); err != nil {
		return Output{}, err
	}
	if pipeline.Transform != nil {
		var err error
		value, err = pipeline.Transform(ctx, value)
		if err != nil {
			return Output{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Output{}, err
	}
	output := Output{Value: value}
	if pipeline.Project != nil {
		output.Projection = pipeline.Project(value)
	}
	if err := ctx.Err(); err != nil {
		return Output{}, err
	}
	return output, nil
}
