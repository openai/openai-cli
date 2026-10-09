package transformers

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAgentsNonterminalAttributionCannotErasePendingWork(t *testing.T) {
	for _, kind := range []string{"created", "in_progress"} {
		status := "queued"
		if kind == "in_progress" {
			status = kind
		}
		child := `{"type":"agent.session.turn.` + kind + `","session_id":"sess_test","turn_id":"pending","turn":{"id":"pending","session_id":"sess_test","subagent_id":"sub_child","status":"` + status + `"}}`
		root := `{"id":"pending","session_id":"sess_test","subagent_id":null,"status":"` + status + `"}`
		for _, tc := range []struct{ name, event string }{
			{"duplicate turn", strings.TrimSuffix(child, "}") + `,"turn":` + root + `}`},
			{"escaped turn", strings.TrimSuffix(child, "}") + `,"\u0074urn":` + root + `}`},
			{"duplicate outer turn ID", strings.Replace(child, `"turn_id":"pending"`, `"turn_id":"pending","turn_id":"other"`, 1)},
			{"duplicate outer session", strings.Replace(child, `"session_id":"sess_test"`, `"session_id":"sess_test","session_id":"other"`, 1)},
			{"duplicate nested role", strings.Replace(child, `"subagent_id":"sub_child"`, `"subagent_id":"sub_child","subagent_id":null`, 1)},
			{"escaped nested role", strings.Replace(child, `"subagent_id":"sub_child"`, `"subagent_id":"sub_child","\u0073ubagent_id":null`, 1)},
			{"case ambiguous role", strings.Replace(child, `"subagent_id":"sub_child"`, `"subagent_id":"sub_child","Subagent_id":null`, 1)},
			{"duplicate nested ID", strings.Replace(child, `"id":"pending"`, `"id":"pending","id":"other"`, 1)},
			{"duplicate nested status", strings.Replace(child, `"status":"`+status+`"`, `"status":"`+status+`","status":"failed"`, 1)},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				for _, route := range []Route{agentsCreateRoute, agentsEventsRoute} {
					var state AgentsStreamState
					state.Observe(agentsTurnEvent("completed", "root", "null"), route)
					state.Observe(agentsTextEvent("delta", "pending_delta", "pending", "msg_pending", "unfinished"), route)
					state.Observe(gjson.Parse(tc.event), route)
					require.Contains(t, state.CompletionError(route), "before the agent turn completed")
					require.Contains(t, state.turns, agentsIdentity("sess_test", "pending"))
					require.Empty(t, state.subagentTurns)
					state.Observe(gjson.Parse(child), route)
					require.Empty(t, state.CompletionError(route), "later unambiguous child attribution must remain valid")
					require.Contains(t, state.subagentTurns, agentsIdentity("sess_test", "pending"))
				}
			})
		}
	}
}

func TestAgentsNonterminalAttributionPreservesConfirmedRoles(t *testing.T) {
	var root AgentsStreamState
	root.Observe(agentsTurnEvent("completed", "root", "null"), agentsEventsRoute)
	contradictoryChild := `{"type":"agent.session.turn.created","session_id":"sess_test","turn_id":"root","turn":{"id":"root","subagent_id":"child","status":"queued"},"turn":{"id":"root","subagent_id":null,"status":"queued"}}`
	root.Observe(gjson.Parse(contradictoryChild), agentsEventsRoute)
	require.Empty(t, root.CompletionError(agentsEventsRoute), "ambiguous noise cannot revoke confirmed root completion")
	require.Empty(t, root.subagentTurns)

	var child AgentsStreamState
	child.Observe(agentsTurnEvent("completed", "root", "null"), agentsEventsRoute)
	child.Observe(agentsTurnEvent("in_progress", "child", `"sub_child"`), agentsEventsRoute)
	contradictoryRoot := `{"type":"agent.session.turn.in_progress","session_id":"sess_test","turn_id":"child","turn":{"id":"child","subagent_id":null,"subagent_id":"sub_child","status":"in_progress"}}`
	child.Observe(gjson.Parse(contradictoryRoot), agentsEventsRoute)
	require.Empty(t, child.CompletionError(agentsEventsRoute), "ambiguous noise cannot change known child attribution")
	require.Contains(t, child.subagentTurns, agentsIdentity("sess_test", "child"))
}
