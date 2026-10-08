package custom

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAgentsFailureDiscriminatorIteratorEarlyErr(t *testing.T) {
	completed := agentsTerminalStatusEvent("completed", "root", `"completed"`, "null")
	const tail = `{"type":"future.usage","marker":"retained tail"}`
	for _, kind := range []string{"error", "agent.session.failed", "agent.session.environment.failed"} {
		for _, key := range []string{`"type"`, `"\u0074ype"`, `"Type"`} {
			t.Run(kind+key, func(t *testing.T) {
				ambiguous := `{"type":"` + kind + `",` + key + `:"future.event","error":{"message":"synthetic detail"}}`
				source := &agentsOwnedStream{values: []gjson.Result{gjson.Parse(ambiguous), gjson.Parse(completed), gjson.Parse(tail)}}
				stream := &agentsStream[outputJSON]{source: source, route: transformers.Route{
					Operation: "(resource) beta.agents.sessions.events > (method) stream", OutputKind: OutputStreamEvent,
				}}
				require.True(t, stream.Next())
				require.NoError(t, stream.Err(), "local-limit-style inspection must not cache an ambiguous failure")
				require.Equal(t, 1, source.reads)
				require.Equal(t, ambiguous, stream.Current().RawJSON())
				require.True(t, stream.Next())
				require.NoError(t, stream.Err())
				require.True(t, stream.Next())
				require.Equal(t, tail, stream.Current().RawJSON())
				require.False(t, stream.Next())
				require.NoError(t, stream.Err(), "later root completion must remain authoritative")
				require.NoError(t, stream.Close())
				require.Equal(t, 1, source.closes)
			})
		}
	}
}

func TestAgentsFailureDiscriminatorPresenterKeepsOutcomesAndBytes(t *testing.T) {
	completed := agentsTerminalStatusEvent("completed", "root", `"completed"`, "null")
	const ambiguous = `{"type":"error","type":"future.event","error":{"message":"synthetic detail"}}`
	const failure = `{"type":"agent.session.failed","session":{"id":"sess_terminal","status":"failed"}}`
	const pending = `{"type":"agent.session.turn.output_text.delta","session_id":"sess_terminal","turn_id":"pending","item_id":"message","content_index":0,"delta":"unfinished"}`
	for _, tc := range []struct {
		name   string
		events []string
		limit  int64
		err    string
	}{
		{"later success", []string{ambiguous, completed}, -1, ""},
		{"unknown EOF", []string{ambiguous}, -1, "without a confirmed agent turn outcome"},
		{"pending EOF", []string{completed, pending, ambiguous}, -1, "before the agent turn completed"},
		{"genuine failure remains", []string{failure, ambiguous, completed}, -1, "the agent session failed"},
		{"later genuine failure", []string{ambiguous, failure, completed}, -1, "the agent session failed"},
		{"local limit ambiguous", []string{ambiguous, completed}, 1, ""},
		{"local limit genuine", []string{failure, completed}, 1, "the agent session failed"},
	} {
		for _, format := range []string{"text", "jsonl", "raw"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				source := &agentsOwnedStream{}
				for _, event := range tc.events {
					source.values = append(source.values, gjson.Parse(event))
				}
				var out, diagnostic bytes.Buffer
				err := ShowJSONIterator(source, tc.limit, ShowJSONOpts{
					Operation: "(resource) beta.agents.sessions.events > (method) stream", OutputKind: OutputStreamEvent,
					Format: format, ExplicitFormat: true, Stdout: &out, Stderr: &diagnostic,
				})
				if tc.err == "" {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, tc.err)
				}
				require.Equal(t, 1, source.closes)
				observed := tc.events
				if tc.limit == 1 {
					observed = observed[:1]
					require.Equal(t, 1, source.reads)
				} else {
					require.Equal(t, len(tc.events)+1, source.reads)
				}
				if format != "text" {
					require.Equal(t, strings.Join(observed, "\n")+"\n", out.String())
					require.Empty(t, diagnostic.String())
				}
			})
		}
	}
	decodeErr := errors.New("synthetic source decoding failure")
	source := &agentsOwnedStream{values: []gjson.Result{gjson.Parse(ambiguous), gjson.Parse(completed)}, readErr: decodeErr}
	var out bytes.Buffer
	err := ShowJSONIterator(source, -1, ShowJSONOpts{
		Operation: "(resource) beta.agents.sessions.events > (method) stream", OutputKind: OutputStreamEvent,
		Format: "jsonl", Stdout: &out,
	})
	require.ErrorIs(t, err, decodeErr)
	require.Equal(t, 1, source.closes)
}
