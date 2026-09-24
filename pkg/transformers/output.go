// Package transformers contains SDK-owned response transformations. Presentation,
// output formats, and terminal sanitization belong to pkg/custom.
package transformers

import (
	"context"

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
// The initial migration deliberately preserves every command's existing output.
func Select(_ Route) Transformer {
	return Identity
}
