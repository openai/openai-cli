package output

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/openai/openai-cli/internal/jsonview"
	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/tidwall/gjson"
)

// IteratorOptions retains the caller's route, transformation, and item boundary.
// Zero Limit consumes no items; negative Limit is unlimited. Callers supply a
// Context and Transform for any iterator they advance.
type IteratorOptions struct {
	Context   context.Context
	Transform transformers.Transformer
	Route     transformers.Route
	Limit     int64
}

// NewIterator shares one lazy transformation boundary between output consumers.
// Construction does not advance the source or inspect its error state.
func NewIterator[T any](source jsonview.Iterator[T], opts IteratorOptions) *Iterator[T] {
	return &Iterator[T]{source: source, context: opts.Context, transform: opts.Transform, route: opts.Route, remaining: opts.Limit}
}

type hasRawJSON interface{ RawJSON() string }

// Transform checks cancellation before and after one response transformation.
func Transform(ctx context.Context, value gjson.Result, transform transformers.Transformer) (gjson.Result, error) {
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	value, err := transform(ctx, value)
	if err != nil {
		return gjson.Result{}, err
	}
	return value, ctx.Err()
}

// JSON carries the same transformed value to direct, paged, and explorer output.
type JSON struct{ gjson.Result }

func (value JSON) RawJSON() string { return value.Raw }

// StreamResultError carries only fixed classifications from the stream
// transformers, never API prose, so the command presenter can retain meaning.
type StreamResultError struct{ Message string }

func (e *StreamResultError) Error() string { return e.Message }

// Iterator shares one lazy transformation boundary across direct output,
// the pager, and the explorer. Current never reruns a transformation.
type Iterator[T any] struct {
	source     jsonview.Iterator[T]
	context    context.Context
	transform  transformers.Transformer
	route      transformers.Route
	remaining  int64
	current    JSON
	err        error
	resultErr  error
	done       bool
	completion transformers.StreamCompletionState

	// The explorer can return while its lazy Next call is still running.
	// Publish only completed steps so Err never reads the source concurrently.
	errorMu     sync.RWMutex
	reportedErr error
}

func (it *Iterator[T]) Next() bool {
	defer func() {
		sourceErr := it.source.Err()
		if it.done && it.err == nil && it.resultErr == nil && sourceErr == nil {
			if message := it.completion.CompletionError(it.route); message != "" {
				it.resultErr = &StreamResultError{message}
			}
		}
		err := errors.Join(it.err, it.resultErr, sourceErr)
		it.errorMu.Lock()
		it.reportedErr = err
		it.errorMu.Unlock()
	}()
	if it.done || it.err != nil || it.resultErr != nil || it.remaining == 0 {
		return false
	}
	if it.err = it.context.Err(); it.err != nil {
		return false
	}
	if !it.source.Next() {
		it.done = true
		it.err = it.context.Err()
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
	// Ordinary result events can signal failure without becoming SDK errors.
	// Keep that original event visible, then stop before consuming another one.
	if message := transformers.StreamFailure(value, it.route); message != "" {
		it.resultErr = &StreamResultError{message}
	}
	it.completion.Observe(value, it.route)
	value, it.err = Transform(it.context, value, it.transform)
	if it.err != nil {
		return false
	}
	it.current = JSON{value}
	if it.remaining > 0 {
		it.remaining--
	}
	return true
}

func (it *Iterator[T]) Current() JSON { return it.current }

func (it *Iterator[T]) Err() error {
	it.errorMu.RLock()
	defer it.errorMu.RUnlock()
	return it.reportedErr
}
