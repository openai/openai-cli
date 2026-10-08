package transformers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAgentsResourceSummariesKeepIDsOutcomesAndArtifactVersions(t *testing.T) {
	for _, test := range []struct{ resource, input, expected string }{
		{"beta.agents", `{"object":"agent","id":"agent_test","name":"Analyst","model":"synthetic","instructions":"private prompt","tools":[],"created_at":1}`, `{"id":"agent_test","name":"Analyst","model":"synthetic"}`},
		{"beta.agents.sessions", `{"object":"agent.session","id":"sess_test","status":"requires_action","required_actions":[{"type":"function_call","call_id":"call_test"}],"agent":{"instructions":"private prompt"},"environment":{"type":"none"},"usage":{"total_tokens":9007199254740993}}`, `{"id":"sess_test","status":"requires_action","required_actions":[{"type":"function_call","call_id":"call_test"}],"usage":{"total_tokens":9007199254740993}}`},
		{"beta.agents.sessions.turns", `{"object":"agent.session.turn","id":"turn_test","session_id":"sess_test","status":"failed","error":{"code":"synthetic_failure","message":"synthetic detail"},"created_at":1}`, `{"id":"turn_test","session_id":"sess_test","status":"failed","error":{"code":"synthetic_failure","message":"synthetic detail"}}`},
		{"beta.agents.sessions.artifacts", `{"object":"agent.session.artifact","id":"artifact_test","session_id":"sess_test","turn_id":"turn_test","path":"/workspace/outputs/report.txt","size_bytes":123,"created_at":1}`, `{"id":"artifact_test","session_id":"sess_test","turn_id":"turn_test","path":"/workspace/outputs/report.txt","size_bytes":123}`},
		{"beta.agents.sessions.traces", `{"object":"agent.session.trace","id":"turn_test","session_id":"sess_test","created_at":1,"otlp":{"resourceSpans":[]}}`, `{"id":"turn_test","session_id":"sess_test","created_at":1}`},
	} {
		t.Run(test.resource, func(t *testing.T) {
			transform := SelectAgentsTransformer(Route{"(resource) " + test.resource + " > (method) retrieve", OutputResponse})
			require.NotNil(t, transform)
			value := gjson.Parse(test.input)
			result, err := transform(context.Background(), value)
			require.NoError(t, err)
			require.Equal(t, test.expected, result.Raw)
			require.Equal(t, test.input, value.Raw)
		})
	}
}

func TestAgentsSummaryPreservesUnknownFieldsAndMalformedShapes(t *testing.T) {
	transform := SelectAgentsTransformer(Route{"(resource) beta.agents.sessions > (method) retrieve", OutputResponse})
	for _, input := range []string{
		`{"object":"agent.session","id":"sess","status":"idle","future":null}`,
		`{"object":"agent.session","id":"sess","status":"idle","status":"failed"}`,
		`{"object":"other","id":"sess","status":"idle"}`,
		`{"object":"agent.session","id":"","status":"idle"}`,
	} {
		result, err := transform(context.Background(), gjson.Parse(input))
		require.NoError(t, err)
		require.Equal(t, input, result.Raw)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := transform(ctx, gjson.Parse(`{}`))
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, SelectAgentsTransformer(agentsEventsRoute))
	require.Nil(t, SelectAgentsTransformer(Route{"(resource) files > (method) retrieve", OutputResponse}))
}
