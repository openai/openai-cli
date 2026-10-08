package transformers

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAgentsFailureDiscriminatorRejectsAmbiguousTypes(t *testing.T) {
	for _, kind := range []string{"error", "agent.session.failed", "agent.session.environment.failed", "agent.session.turn.failed", "agent.session.turn.cancelled"} {
		for _, suffix := range []string{
			`,"type":"future.event"`,
			`,"\u0074ype":"future.event"`,
			`,"Type":"future.event"`,
			`,"TYPE":"future.event"`,
			`,"type":null`,
		} {
			t.Run(kind+suffix, func(t *testing.T) {
				value := gjson.Parse(`{"type":"` + kind + `"` + suffix + `,"session_id":"sess_test","turn_id":"root","turn":{"id":"root","status":"` + strings.TrimPrefix(kind, "agent.session.turn.") + `","subagent_id":null},"error":{"message":"synthetic detail"}}`)
				for _, route := range []Route{agentsCreateRoute, agentsEventsRoute} {
					if got := AgentsStreamFailure(value, route); got != "" {
						t.Errorf("ambiguous discriminator established failure: %q", got)
					}
					var state AgentsStreamState
					state.Observe(value, route)
					state.Observe(agentsTurnEvent("completed", "root", "null"), route)
					require.Empty(t, state.CompletionError(route), "ambiguous failure must not replace later confirmed success")
				}
			})
		}
	}
}

func TestAgentsFailureDiscriminatorPreservesGenuineFailures(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"error", `{"type":"error","error":{"message":"synthetic"}}`, "the Agents API reported an error while streaming"},
		{"escaped single type", `{"\u0074ype":"error","error":{"message":"synthetic"}}`, "the Agents API reported an error while streaming"},
		{"session", `{"type":"agent.session.failed","session":{"id":"sess_test","status":"failed"}}`, "the agent session failed"},
		{"environment", `{"type":"agent.session.environment.failed","session_id":"sess_test","environment":{"id":"env_test","type":"self_hosted","status":"failed","error":null}}`, "the agent environment failed"},
		{"turn", agentsTurnEvent("failed", "root", "null").Raw, "the agent turn failed"},
		{"cancelled", agentsTurnEvent("cancelled", "root", "null").Raw, "the agent turn was cancelled"},
		{"completed", agentsTurnEvent("completed", "root", "null").Raw, ""},
		{"subagent", agentsTurnEvent("failed", "child", `"sub_child"`).Raw, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := gjson.Parse(tc.raw)
			require.Equal(t, tc.want, AgentsStreamFailure(value, agentsEventsRoute))
			var state AgentsStreamState
			state.Observe(value, agentsEventsRoute)
			state.Observe(gjson.Parse(`{"type":"error","type":"future.event"}`), agentsEventsRoute)
			state.Observe(agentsTurnEvent("completed", "later", "null"), agentsEventsRoute)
			require.Equal(t, tc.want, state.CompletionError(agentsEventsRoute))
		})
	}
	// Keep the existing direct malformed-error policy. Source decoding errors
	// independently remain authoritative in the owning iterator.
	malformed := gjson.Parse(`{"type":"error"`)
	require.False(t, gjson.Valid(malformed.Raw))
	require.Equal(t, "the Agents API reported an error while streaming", AgentsStreamFailure(malformed, agentsEventsRoute))
}

func TestAgentsFailureDiscriminatorPreservesUnconfirmedWork(t *testing.T) {
	for _, raw := range []string{
		`{"type":"error","type":"future.event"}`,
		`{"type":null}`, `{"type":7}`, `{"type":true}`, `{"type":["error"]}`, `{"type":{"kind":"error"}}`,
	} {
		value := gjson.Parse(raw)
		require.Empty(t, AgentsStreamFailure(value, agentsEventsRoute))
		var state AgentsStreamState
		state.Observe(value, agentsEventsRoute)
		require.Contains(t, state.CompletionError(agentsEventsRoute), "without a confirmed agent turn outcome")
		state.Observe(agentsTurnEvent("completed", "root", "null"), agentsEventsRoute)
		state.Observe(agentsTextEvent("delta", "pending", "pending_turn", "message", "unfinished"), agentsEventsRoute)
		state.Observe(value, agentsEventsRoute)
		require.Contains(t, state.CompletionError(agentsEventsRoute), "before the agent turn completed")
		state.Observe(agentsTurnEvent("completed", "pending_turn", "null"), agentsEventsRoute)
		require.Empty(t, state.CompletionError(agentsEventsRoute))
	}
}

func TestAgentsFailureDiscriminatorKeepsCreationAmbiguityStrict(t *testing.T) {
	ambiguous := gjson.Parse(`{"type":"error","type":"future.event"}`)
	acknowledgement := gjson.Parse(`{"type":"agent.session.created","session":{"id":"sess_test","status":"idle","error":null,"environment":{"id":"env_test","type":"self_hosted"}}}`)
	for _, values := range [][]gjson.Result{{ambiguous, acknowledgement}, {acknowledgement, ambiguous}} {
		state := AgentsStreamState{AllowNoInputSessionCreation: true}
		for _, value := range values {
			state.Observe(value, agentsCreateRoute)
		}
		require.Empty(t, state.failure)
		require.True(t, state.creation.invalid, "type ambiguity must still disable the no-input shortcut")
		require.Contains(t, state.CompletionError(agentsCreateRoute), "without a confirmed agent turn outcome")
		state.Observe(agentsTurnEvent("completed", "root", "null"), agentsCreateRoute)
		require.Empty(t, state.CompletionError(agentsCreateRoute), "later genuine root completion remains valid")
	}
}
