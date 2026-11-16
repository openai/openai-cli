package output

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type transformTestIterator struct {
	items []any
	index int
	calls int
	err   error
}

func (it *transformTestIterator) Next() bool {
	it.calls++
	if it.index == len(it.items) {
		return false
	}
	it.index++
	return true
}
func (it *transformTestIterator) Current() any { return it.items[it.index-1] }
func (it *transformTestIterator) Err() error   { return it.err }

func TestOutputIteratorSharedExplorerBoundary(t *testing.T) {
	// The explorer reads RawJSON and can read Current repeatedly; it receives
	// the same transformed value as other formats without applying the explicit
	// GJSON presentation path (the explorer historically displays the whole item).
	source := &transformTestIterator{items: []any{map[string]any{"original": true}, map[string]any{"unvisited": true}}}
	calls := 0
	iter := NewIterator(source, IteratorOptions{
		Context: context.Background(),
		Limit:   1,
		Transform: func(context.Context, gjson.Result) (gjson.Result, error) {
			calls++
			return gjson.Parse(`{"nested":{"changed":true}}`), nil
		},
	})
	require.Equal(t, 0, source.calls)
	require.True(t, iter.Next())
	require.Equal(t, `{"nested":{"changed":true}}`, iter.Current().RawJSON())
	require.Equal(t, `{"nested":{"changed":true}}`, iter.Current().RawJSON())
	require.False(t, iter.Next())
	require.NoError(t, iter.Err())
	require.Equal(t, 1, calls)
	require.Equal(t, 1, source.calls)
}

// blockingErrorIterator models a network iterator whose error is only safe to
// inspect after Next completes, as is the case during lazy explorer loading.
type blockingErrorIterator struct {
	started  chan struct{}
	release  chan struct{}
	failure  error
	err      error
	errReads atomic.Int32
}

func (it *blockingErrorIterator) Next() bool {
	close(it.started)
	<-it.release
	it.err = it.failure
	return false
}

func (it *blockingErrorIterator) Current() any { return nil }
func (it *blockingErrorIterator) Err() error {
	it.errReads.Add(1)
	return it.err
}

func TestOutputIteratorErrDoesNotWaitForOrReadActiveNext(t *testing.T) {
	failure := errors.New("synthetic lazy page failure")
	source := &blockingErrorIterator{
		started: make(chan struct{}), release: make(chan struct{}), failure: failure,
	}
	iter := NewIterator(source, IteratorOptions{
		Context:   context.Background(),
		Limit:     -1,
		Transform: transformers.Identity,
	})
	nextDone := make(chan bool, 1)
	go func() { nextDone <- iter.Next() }()
	<-source.started
	t.Cleanup(func() {
		select {
		case <-source.release:
		default:
			close(source.release)
		}
	})

	// Quitting the explorer must neither wait for a network read nor ask the
	// underlying iterator for state that its Next goroutine still owns.
	errorRead := make(chan error, 1)
	go func() { errorRead <- iter.Err() }()
	select {
	case err := <-errorRead:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Err waited for the active Next call")
	}
	require.Zero(t, source.errReads.Load(), "Err must read only the completed-step snapshot")

	close(source.release)
	require.False(t, <-nextDone)
	require.ErrorIs(t, iter.Err(), failure)
	require.ErrorIs(t, iter.Err(), failure)
	require.Equal(t, int32(1), source.errReads.Load(), "only Next may read the source error")
}

type countedIterator struct {
	reads int
	err   error
}

func (i *countedIterator) Next() bool   { i.reads++; return i.reads <= 5 && i.err == nil }
func (i *countedIterator) Current() int { return i.reads }
func (i *countedIterator) Err() error   { return i.err }

func TestInteractiveIteratorLimitStopsUpstreamReads(t *testing.T) {
	for _, limit := range []int64{0, 1, 3, -1} {
		upstream := &countedIterator{}
		iter := NewIterator(upstream, IteratorOptions{
			Context:   context.Background(),
			Transform: transformers.Identity,
			Limit:     limit,
		})
		var values []int
		for iter.Next() {
			values = append(values, int(iter.Current().Int()))
		}
		if limit >= 0 {
			require.Len(t, values, int(limit))
			require.Equal(t, int(limit), upstream.reads, "must not fetch a page beyond the requested limit")
			require.False(t, iter.Next())
			require.Equal(t, int(limit), upstream.reads)
		} else {
			require.Equal(t, []int{1, 2, 3, 4, 5}, values)
		}
	}
}
