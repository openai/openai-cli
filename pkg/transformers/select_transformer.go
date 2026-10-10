// Package transformers contains SDK-owned response transformations. Presentation
// and output formats belong to pkg/custom and its internal rendering helpers.
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
// Only known API fields are summarized; unfamiliar routes preserve their values.
func Select(route Route) Transformer {
	switch route {
	case Route{"(resource) admin.organization.projects.rate_limits > (method) list_rate_limits", OutputPageItem},
		Route{"(resource) admin.organization.projects.rate_limits > (method) update_rate_limit", OutputResponse}:
		return ProjectRateLimit
	}
	return selectReadableTransformer(route)
}
