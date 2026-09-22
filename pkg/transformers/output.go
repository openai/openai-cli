// Package transformers contains SDK-owned response transformations and optional
// terminal renderers. Output-mode selection and JSON presentation belong to pkg/custom.
package transformers

import (
	"context"
	"os"

	"github.com/tidwall/gjson"
)

// OutputKind identifies the successful-response boundary being presented.
type OutputKind string

const (
	OutputUnspecified OutputKind = ""
	OutputResponse    OutputKind = "response"
	OutputPageItem    OutputKind = "page_item"
	OutputStreamEvent OutputKind = "stream_event"
)

// Route identifies an operation independently of its presentation format.
type Route struct {
	Operation  string
	OutputKind OutputKind
}

// Transformer changes one response or iterator value before presentation.
// Implementations must respect cancellation and must not consume other values.
type Transformer func(context.Context, gjson.Result) (gjson.Result, error)

// Identity preserves the original response, including its raw JSON bytes.
func Identity(_ context.Context, value gjson.Result) (gjson.Result, error) {
	return value, nil
}

// Select is the SDK-owned hook for command-specific default transformations.
// Readable presentation remains separate from the original API value.
func Select(route Route) Transformer {
	if IsImageOperation(route.Operation) {
		switch route.OutputKind {
		case OutputResponse:
			return imageOutputOnly(ImageResponse)
		case OutputStreamEvent:
			return imageOutputOnly(ImageStreamEvent)
		}
	}
	return Identity
}

// TerminalRenderer presents a default response directly to a terminal. A false
// result leaves the response available for ordinary JSON presentation.
type TerminalRenderer func(context.Context, gjson.Result, *os.File) (bool, error)

// SelectTerminal selects an optional renderer after the caller has established
// that stdout is a terminal and the user has not requested an explicit format.
func SelectTerminal(route Route) TerminalRenderer {
	if route.Operation == "(resource) images > (method) generate" && route.OutputKind == OutputResponse {
		return renderGeneratedImages
	}
	return nil
}
