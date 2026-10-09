package custom

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/stretchr/testify/require"
)

const agentsExplorerFailure = `{"type":"agent.session.turn.failed","session_id":"sess_test","turn_id":"turn_test","turn":{"id":"turn_test","subagent_id":null,"status":"failed"}}`

func agentsExplorerRecords() []string {
	values := []string{agentsExplorerFailure}
	for i := 1; i < 30; i++ {
		values = append(values, fmt.Sprintf(`{"type":"future.usage","sequence":%d}`, i))
	}
	return values
}

func agentsExplorerOutput(values []string, limit int64) *outputIterator[any] {
	route := transformers.Route{Operation: "(resource) beta.agents.sessions.events > (method) stream", OutputKind: OutputStreamEvent}
	return &outputIterator[any]{
		source:  &agentsStream[any]{source: streamItems(values...), route: route},
		context: context.Background(), transform: transformers.Identity, route: route, remaining: limit,
	}
}

func TestAgentsExplorerKeepsFailureTailAvailableAfterPreload(t *testing.T) {
	values := agentsExplorerRecords()
	original := agentsExplorerOutput(values, -1)
	explorer := &agentsExplorerStream{source: original}
	for i, value := range values {
		require.True(t, explorer.Next(), "record %d", i)
		require.Equal(t, value, explorer.Current().RawJSON())
		require.NoError(t, explorer.Err(), "preload must not interpret an observed outcome as EOF")
		var failure *streamResultError
		require.ErrorAs(t, original.Err(), &failure, "local explorer exit must retain the observed failure")
		require.Equal(t, "the agent turn failed", failure.message)
	}
	require.False(t, explorer.Next())
	require.ErrorContains(t, explorer.Err(), "the agent turn failed")
}

func TestAgentsExplorerRetainsFailureAtLocalLimit(t *testing.T) {
	for _, limit := range []int64{1, 19, 20, 21} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			original := agentsExplorerOutput(agentsExplorerRecords(), limit)
			explorer := &agentsExplorerStream{source: original}
			var count int64
			for explorer.Next() {
				count++
			}
			require.Equal(t, limit, count)
			require.ErrorContains(t, explorer.Err(), "the agent turn failed")
			require.ErrorContains(t, original.Err(), "the agent turn failed")
		})
	}
}

func TestAgentsExplorerKeepsErrorsWhenNextStops(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("synthetic transport failure")} {
		original := &agentsOwnedStream{readErr: cause}
		explorer := &agentsExplorerStream{source: original}
		require.False(t, explorer.Next())
		require.ErrorIs(t, explorer.Err(), cause)
	}
}
