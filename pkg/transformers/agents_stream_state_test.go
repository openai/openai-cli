package transformers

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

var agentsCreateRoute = Route{"(resource) beta.agents.sessions > (method) create", OutputStreamEvent}
var agentsEventsRoute = Route{"(resource) beta.agents.sessions.events > (method) stream", OutputStreamEvent}

func agentsTurnEvent(status, turn, subagent string) gjson.Result {
	return gjson.Parse(fmt.Sprintf(`{"type":"agent.session.turn.%s","session_id":"sess_test","turn_id":%q,"turn":{"id":%q,"status":%q,"subagent_id":%s}}`, status, turn, turn, status, subagent))
}

func TestAgentsStreamCompletionRequiresRootOutcome(t *testing.T) {
	for _, route := range []Route{agentsCreateRoute, agentsEventsRoute} {
		for _, test := range []struct {
			name   string
			events []gjson.Result
			ok     bool
		}{
			{"empty", nil, false},
			{"idle", []gjson.Result{gjson.Parse(`{"type":"agent.session.idle"}`)}, false},
			{"unknown", []gjson.Result{gjson.Parse(`{"type":"future.event"}`)}, false},
			{"subagent completed", []gjson.Result{agentsTurnEvent("completed", "subturn", `"sub_1"`)}, false},
			{"root pending", []gjson.Result{agentsTurnEvent("created", "turn_1", "null")}, false},
			{"root completed", []gjson.Result{agentsTurnEvent("completed", "turn_1", "null")}, true},
			{"late progress", []gjson.Result{agentsTurnEvent("completed", "turn_1", "null"), agentsTurnEvent("in_progress", "turn_1", "null")}, true},
			{"next turn pending", []gjson.Result{agentsTurnEvent("completed", "turn_1", "null"), agentsTurnEvent("created", "turn_2", "null")}, false},
			{"next unclassified turn pending", []gjson.Result{agentsTurnEvent("completed", "turn_1", "null"), agentsTextEvent("delta", "ev", "turn_2", "msg", "unfinished")}, false},
			{"next command-only turn pending", []gjson.Result{agentsTurnEvent("completed", "turn_1", "null"), gjson.Parse(`{"type":"agent.output.command_execution_output.delta","session_id":"sess_test","turn_id":"turn_2","item_id":"shell","delta":"unfinished"}`)}, false},
			{"subagent progress after root", []gjson.Result{agentsTurnEvent("completed", "turn_1", "null"), agentsTextEvent("delta", "ev", "subturn", "msg", "activity"), agentsTurnEvent("in_progress", "subturn", `"sub_1"`)}, true},
			{"concurrent turn pending", []gjson.Result{agentsTurnEvent("created", "turn_1", "null"), agentsTurnEvent("completed", "turn_2", "null")}, false},
			{"subagent failed then root completed", []gjson.Result{agentsTurnEvent("failed", "subturn", `"sub_1"`), agentsTurnEvent("completed", "turn_1", "null")}, true},
			{"root failed then idle", []gjson.Result{agentsTurnEvent("failed", "turn_1", "null"), gjson.Parse(`{"type":"agent.session.idle"}`)}, false},
			{"root failure remains", []gjson.Result{agentsTurnEvent("failed", "turn_1", "null"), agentsTurnEvent("completed", "turn_2", "null")}, false},
		} {
			t.Run(route.Operation+"/"+test.name, func(t *testing.T) {
				var state AgentsStreamState
				for _, event := range test.events {
					state.Observe(event, route)
				}
				require.Equal(t, test.ok, state.CompletionError(route) == "")
			})
		}
	}
}

func TestAgentsStreamFailureDoesNotConfuseToolAndTurnFailures(t *testing.T) {
	for _, test := range []struct{ event, message string }{
		{`{"type":"error","error":{"message":"private detail"}}`, "the Agents API reported an error while streaming"},
		{`{"type":"agent.session.failed"}`, "the agent session failed"},
		{`{"type":"agent.session.environment.failed"}`, "the agent environment failed"},
		{`{"type":"agent.session.turn.item.done","item":{"type":"function_call","status":"failed"}}`, ""},
		{agentsTurnEvent("failed", "turn_1", `"sub_1"`).Raw, ""},
		{agentsTurnEvent("failed", "turn_1", "null").Raw, "the agent turn failed"},
		{agentsTurnEvent("cancelled", "turn_1", "null").Raw, "the agent turn was cancelled"},
	} {
		require.Equal(t, test.message, AgentsStreamFailure(gjson.Parse(test.event), agentsEventsRoute))
		require.Empty(t, AgentsStreamFailure(gjson.Parse(test.event), streamTestRoute("responses")))
	}
}

func TestAgentsMalformedTerminalCannotEstablishSuccess(t *testing.T) {
	for _, event := range []string{
		`{"type":"agent.session.turn.completed","session_id":"sess","turn_id":"turn","turn":{}}`,
		`{"type":"agent.session.turn.completed","session_id":"sess","turn_id":"turn","turn":{"subagent_id":null,"id":"other"}}`,
		`{"type":"agent.session.turn.completed","session_id":"sess","turn_id":"turn","turn":{"subagent_id":null,"status":"failed"}}`,
		`{"type":"agent.session.turn.completed","turn_id":"turn","turn":{"subagent_id":null}}`,
	} {
		var state AgentsStreamState
		state.Observe(gjson.Parse(event), agentsCreateRoute)
		require.NotEmpty(t, state.CompletionError(agentsCreateRoute))
		require.False(t, AgentsRootTurnTerminal(gjson.Parse(event), agentsCreateRoute))
	}
}

func TestAgentsSubagentCountDoesNotLimitSuccessfulOutcomes(t *testing.T) {
	for _, count := range []int{127, 128, 129, 4096} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var state AgentsStreamState
			for i := 0; i < count; i++ {
				state.Observe(agentsTurnEvent("in_progress", fmt.Sprint(i), `"sub_test"`), agentsEventsRoute)
			}
			require.Empty(t, state.turns, "known subagents never occupy root outcome storage")
			for i := 0; i < count; i++ {
				state.Observe(agentsTurnEvent("completed", fmt.Sprint(i), `"sub_test"`), agentsEventsRoute)
			}
			state.Observe(agentsTurnEvent("completed", "root", "null"), agentsEventsRoute)
			require.Empty(t, state.CompletionError(agentsEventsRoute))
			require.Len(t, state.turns, 1)
		})
	}
}

func TestAgentsLargeRootHistoryPreservesLateOutcomes(t *testing.T) {
	var state AgentsStreamState
	const count = 4096
	for i := 0; i < count; i++ {
		state.Observe(agentsTurnEvent("in_progress", fmt.Sprint(i), "null"), agentsEventsRoute)
	}
	for i := count - 1; i >= 0; i-- {
		state.Observe(agentsTurnEvent("completed", fmt.Sprint(i), "null"), agentsEventsRoute)
	}
	state.Observe(agentsTurnEvent("in_progress", "0", "null"), agentsEventsRoute)
	require.Empty(t, state.CompletionError(agentsEventsRoute))
	state.Observe(agentsTextEvent("delta", "new", "new_root", "item", "unfinished"), agentsEventsRoute)
	require.Contains(t, state.CompletionError(agentsEventsRoute), "before the agent turn completed")
	state.Observe(agentsTurnEvent("completed", "new_root", "null"), agentsEventsRoute)
	require.Empty(t, state.CompletionError(agentsEventsRoute))
	require.Empty(t, state.CompletionError(streamTestRoute("responses")))
	require.NotEqual(t, agentsIdentity("a\x00b", "c"), agentsIdentity("a", "b\x00c"))
}

func TestAgentsUnknownTurnsBecomeSubagentsWithoutFalsePendingRoots(t *testing.T) {
	var state AgentsStreamState
	state.Observe(agentsTurnEvent("completed", "root", "null"), agentsEventsRoute)
	const count = 1024
	for i := 0; i < count; i++ {
		state.Observe(agentsTextEvent("delta", fmt.Sprint(i), fmt.Sprint(i), "item", "text"), agentsEventsRoute)
	}
	require.NotEmpty(t, state.CompletionError(agentsEventsRoute))
	for i := count - 1; i >= 0; i-- {
		state.Observe(agentsTurnEvent("in_progress", fmt.Sprint(i), `"sub_test"`), agentsEventsRoute)
	}
	for i := 0; i < count; i++ {
		state.Observe(agentsTextEvent("delta", "late"+fmt.Sprint(i), fmt.Sprint(i), "item", "late text"), agentsEventsRoute)
	}
	require.Empty(t, state.CompletionError(agentsEventsRoute))
	require.Len(t, state.turns, 1)
}

func TestAgentsStreamRetainsFirstFailureWhileObservingTrailingRecords(t *testing.T) {
	var state AgentsStreamState
	state.Observe(agentsTurnEvent("failed", "root", "null"), agentsEventsRoute)
	state.Observe(gjson.Parse(`{"type":"agent.session.failed"}`), agentsEventsRoute)
	state.Observe(agentsTurnEvent("completed", "other_root", "null"), agentsEventsRoute)
	require.Equal(t, "the agent turn failed", state.CompletionError(agentsEventsRoute))
	require.Len(t, state.turns, 2)
}
