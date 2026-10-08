package custom

import (
	"errors"
	"testing"

	"github.com/openai/openai-cli/pkg/transformers"
)

func TestAgentsStreamFailurePreservesTrailingRecords(t *testing.T) {
	const failure = `{"type":"agent.session.turn.failed","session_id":"sess_test","turn_id":"turn_test","turn":{"id":"turn_test","subagent_id":null,"status":"failed"}}`
	const tail = `{"type":"future.usage","retained":true}`
	source := streamItems(failure, tail)
	stream := &agentsStream[any]{source: source, route: transformers.Route{
		Operation: "(resource) beta.agents.sessions.events > (method) stream", OutputKind: OutputStreamEvent,
	}}
	if !stream.Next() || stream.Err() == nil {
		t.Fatal("observed failure was not exposed before EOF")
	}
	if !stream.Next() || stream.Current().(outputJSON).RawJSON() != tail {
		t.Fatal("failure truncated trailing record")
	}
	if stream.Next() {
		t.Fatal("unexpected extra record")
	}
	var outcome *streamResultError
	if !errors.As(stream.Err(), &outcome) || outcome.message != "the agent turn failed" {
		t.Fatalf("outcome=%v", stream.Err())
	}
}
