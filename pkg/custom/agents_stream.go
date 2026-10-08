package custom

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/tidwall/gjson"
)

// agentsStream observes outcomes without ending iteration at a terminal turn.
// Err exposes an observed failure even when a local limit or output failure stops observation.
type agentsStream[T any] struct {
	source  jsonview.Iterator[T]
	route   transformers.Route
	state   transformers.AgentsStreamState
	ended   bool
	err     error
	failure error
}

func (s *agentsStream[T]) Next() bool {
	if s.ended || s.err != nil {
		return false
	}
	if !s.source.Next() {
		s.ended = true
		return false
	}
	current := s.source.Current()
	var value gjson.Result
	if raw, ok := any(current).(hasRawJSON); ok {
		value = gjson.Parse(raw.RawJSON())
	} else {
		encoded, err := json.Marshal(current)
		if err != nil {
			s.err = err
			return false
		}
		value = gjson.ParseBytes(encoded)
	}
	s.state.Observe(value, s.route)
	if s.failure == nil {
		if message := transformers.AgentsStreamFailure(value, s.route); message != "" {
			s.failure = &streamResultError{message}
		}
	}
	return true
}

func (s *agentsStream[T]) Current() T { return s.source.Current() }

func (s *agentsStream[T]) Err() error {
	err := errors.Join(s.err, s.failure, s.source.Err())
	if err == nil && s.ended {
		if message := s.state.CompletionError(s.route); message != "" {
			return &streamResultError{message}
		}
	}
	return err
}

func (s *agentsStream[T]) Close() error {
	if closer, ok := s.source.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}
