package custom

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/openai/openai-cli/internal/jsonview"
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

func selectOutputTransformer(opts ShowJSONOpts, selectTransformer transformerSelector) transformers.Transformer {
	// Explicit presentation choices operate on the original API response.
	// Missing or non-success routing metadata also uses identity, including errors.
	if opts.ExplicitFormat || opts.Transform != "" || opts.RawOutput || opts.Operation == "" {
		return transformers.Identity
	}
	switch opts.OutputKind {
	case OutputResponse, OutputPageItem, OutputStreamEvent:
		return selectTransformer(transformers.Route{Operation: opts.Operation, OutputKind: opts.OutputKind})
	default:
		return transformers.Identity
	}
}

func transformOutput(ctx context.Context, value gjson.Result, transform transformers.Transformer) (gjson.Result, error) {
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	value, err := transform(ctx, value)
	if err != nil {
		return gjson.Result{}, err
	}
	return value, ctx.Err()
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

// outputIterator shares one lazy transformation boundary across direct output,
// the pager, and the explorer. Current never reruns a transformation.
type outputIterator[T any] struct {
	source    jsonview.Iterator[T]
	context   context.Context
	transform transformers.Transformer
	remaining int64
	current   outputJSON
	err       error
	done      bool

	// The explorer can return while its lazy Next call is still running.
	// Publish only completed steps so Err never reads the source concurrently.
	errorMu     sync.RWMutex
	reportedErr error
}

func (it *outputIterator[T]) Next() bool {
	defer func() {
		err := errors.Join(it.err, it.source.Err())
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
	value, it.err = transformOutput(it.context, value, it.transform)
	if it.err != nil {
		return false
	}
	it.current = outputJSON{value}
	if it.remaining > 0 {
		it.remaining--
	}
	return true
}

func (it *outputIterator[T]) Current() outputJSON { return it.current }

func (it *outputIterator[T]) Err() error {
	it.errorMu.RLock()
	defer it.errorMu.RUnlock()
	return it.reportedErr
}
