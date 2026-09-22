package custom

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/tidwall/gjson"
)

type OutputKind = transformers.OutputKind

const (
	OutputUnspecified = transformers.OutputUnspecified
	OutputResponse    = transformers.OutputResponse
	OutputPageItem    = transformers.OutputPageItem
	OutputStreamEvent = transformers.OutputStreamEvent
)

type transformerSelector func(transformers.Route) transformers.Transformer

// Default transformations run only for successfully routed API output. Explicit
// data formats and extraction keep the original API response.
func usesDefaultOutput(opts ShowJSONOpts) bool {
	format := strings.ToLower(opts.Format)
	readableFormat := format == "" || format == "auto" || format == "text"
	if opts.ExplicitFormat && !readableFormat || opts.Transform != "" || opts.RawOutput || opts.Operation == "" {
		return false
	}
	switch opts.OutputKind {
	case OutputResponse, OutputPageItem, OutputStreamEvent:
		return true
	default:
		return false
	}
}

func selectOutputTransformer(opts ShowJSONOpts, selectTransformer transformerSelector) transformers.Transformer {
	if !usesDefaultOutput(opts) {
		return transformers.Identity
	}
	return selectTransformer(transformers.Route{Operation: opts.Operation, OutputKind: opts.OutputKind})
}

func readableOutputRoute(opts ShowJSONOpts) transformers.Route {
	if usesDefaultOutput(opts) && resolvedOutputFormat(opts) == "text" {
		return transformers.Route{Operation: opts.Operation, OutputKind: opts.OutputKind}
	}
	return transformers.Route{}
}

func transformOutput(ctx context.Context, value gjson.Result, transform transformers.Transformer) (gjson.Result, error) {
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	if transform != nil {
		var err error
		value, err = transform(ctx, value)
		if err != nil {
			return gjson.Result{}, err
		}
	}
	return value, ctx.Err()
}

// Prepare the concrete readable view beside the API value. Rendering metadata
// is never injected into JSON, and neither step is repeated by Current/RawJSON.
func prepareOutput(ctx context.Context, value gjson.Result, transform transformers.Transformer, route transformers.Route) (preparedOutput, error) {
	value, err := transformOutput(ctx, value, transform)
	if err != nil {
		return preparedOutput{}, err
	}
	output := preparedOutput{Value: value}
	if route.Operation != "" {
		output.View = readable.Project(value, route)
	}
	if err := ctx.Err(); err != nil {
		return preparedOutput{}, err
	}
	return output, nil
}

func applyJSONPath(value gjson.Result, path string) gjson.Result {
	if path != "" {
		if extracted := value.Get(path); extracted.Exists() {
			return extracted
		}
	}
	return value
}

type outputJSON struct{ gjson.Result }

func (value outputJSON) RawJSON() string { return value.Raw }

// Presentation metadata is carried in Go values, never injected into API JSON.
// Repeated Current/RawJSON calls cannot select or run a transformation again.
type preparedOutput struct {
	Value gjson.Result
	View  readable.View
}

func (value preparedOutput) RawJSON() string { return value.Value.Raw }

// outputIterator shares one lazy transformation boundary across direct output,
// the pager, and the explorer. Current never reruns a transformation.
type outputIterator[T any] struct {
	source     jsonview.Iterator[T]
	context    context.Context
	transform  transformers.Transformer
	route      transformers.Route
	remaining  int64
	outputKind OutputKind
	current    preparedOutput
	err        error
	resultErr  error
	done       bool

	// The explorer can return while its lazy Next call is still running.
	// Publish only completed steps so Err never reads the source concurrently.
	errorMu     sync.RWMutex
	reportedErr error
}

func (it *outputIterator[T]) Next() bool {
	defer func() {
		err := errors.Join(it.err, it.resultErr, it.source.Err())
		it.errorMu.Lock()
		it.reportedErr = err
		it.errorMu.Unlock()
	}()
	if it.done || it.err != nil || it.remaining == 0 {
		return false
	}
	if it.err = it.context.Err(); it.err != nil {
		return false
	}
	if !it.source.Next() {
		it.done = true
		return false
	}
	if it.err = it.context.Err(); it.err != nil {
		return false
	}
	item := it.source.Current()
	var value gjson.Result
	if raw, ok := any(item).(hasRawJSON); ok {
		value = gjson.Parse(raw.RawJSON())
	} else {
		var encoded []byte
		encoded, it.err = json.Marshal(item)
		if it.err != nil {
			return false
		}
		value = gjson.ParseBytes(encoded)
	}
	if it.outputKind == OutputStreamEvent && it.resultErr == nil {
		if message := transformers.StreamFailure(value); message != "" {
			it.resultErr = errors.New(message)
		}
	}
	output, err := prepareOutput(it.context, value, it.transform, it.route)
	it.err = err
	if it.err != nil {
		return false
	}
	it.current = output
	if it.remaining > 0 {
		it.remaining--
	}
	return true
}

func (it *outputIterator[T]) Current() preparedOutput { return it.current }

func (it *outputIterator[T]) Err() error {
	it.errorMu.RLock()
	defer it.errorMu.RUnlock()
	return it.reportedErr
}
