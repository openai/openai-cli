package transformers

import (
	"fmt"
	"strings"
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

func TestAgentsTerminalStatusMustMatchOutcome(t *testing.T) {
	for _, terminal := range []string{"completed", "failed", "cancelled"} {
		for _, tc := range []struct {
			name, status string
			valid        bool
		}{
			{"matching", fmt.Sprintf("%q", terminal), true},
			{"missing", "", false},
			{"null", "null", false},
			{"number", "0", false},
			{"boolean", "false", false},
			{"array", `["` + terminal + `"]`, false},
			{"object", `{"value":"` + terminal + `"}`, false},
			{"empty", `""`, false},
			{"in progress", `"in_progress"`, false},
			{"unknown", `"future_status"`, false},
			{"other terminal", fmt.Sprintf("%q", map[string]string{"completed": "failed", "failed": "cancelled", "cancelled": "completed"}[terminal]), false},
		} {
			t.Run(terminal+"/"+tc.name, func(t *testing.T) {
				event := agentsTurnEvent(terminal, "root", "null").Raw
				replacement := ""
				if tc.status != "" {
					replacement = `"status":` + tc.status + `,`
				}
				event = strings.Replace(event, `"status":"`+terminal+`",`, replacement, 1)
				value := gjson.Parse(event)
				for _, route := range []Route{agentsCreateRoute, agentsEventsRoute} {
					if got := AgentsRootTurnTerminal(value, route); got != tc.valid {
						t.Errorf("terminal classifier = %t; want %t", got, tc.valid)
					}
					var state AgentsStreamState
					state.Observe(value, route)
					if state.completed != tc.valid {
						t.Errorf("state confirmed terminal = %t; want %t", state.completed, tc.valid)
					}
					if !tc.valid {
						require.Contains(t, state.CompletionError(route), "before the agent turn completed")
						require.Empty(t, AgentsStreamFailure(value, route), "malformed status must not manufacture a specific root failure")
					} else if terminal == "completed" {
						require.Empty(t, state.CompletionError(route))
					} else {
						require.NotEmpty(t, state.CompletionError(route))
					}
					require.Equal(t, event, value.Raw)
				}
			})
		}
	}
}

func TestAgentsTerminalMalformedJSONCannotClassify(t *testing.T) {
	valid := agentsTurnEvent("completed", "root", "null").Raw
	for _, event := range []string{
		strings.TrimSuffix(valid, "}"),
		strings.Replace(valid, `"subagent_id":null}`, `"subagent_id":null,}`, 1),
		strings.Replace(valid, `"turn":{`, `"turn":[{`, 1),
	} {
		value := gjson.Parse(event)
		require.False(t, gjson.Valid(value.Raw))
		require.False(t, AgentsRootTurnTerminal(value, agentsCreateRoute))
		var state AgentsStreamState
		state.Observe(value, agentsCreateRoute)
		require.NotEmpty(t, state.CompletionError(agentsCreateRoute))
	}
}

func TestAgentsTerminalAmbiguousFieldsCannotConfirmOutcome(t *testing.T) {
	valid := strings.Replace(agentsTurnEvent("completed", "root", "null").Raw, `"turn":{`, `"turn":{"session_id":"sess_test",`, 1)
	for _, tc := range []struct{ field, replacement string }{
		{`"type":"agent.session.turn.completed"`, `"type":"agent.session.turn.completed","type":"agent.session.turn.failed"`},
		{`"type":"agent.session.turn.completed"`, `"type":"agent.session.turn.completed","\u0074ype":"agent.session.turn.failed"`},
		{`"type":"agent.session.turn.completed"`, `"type":"agent.session.turn.completed","Type":"agent.session.turn.failed"`},
		{`"status":"completed"`, `"status":"completed","status":"failed"`},
		{`"status":"completed"`, `"status":"completed","\u0073tatus":"failed"`},
		{`"status":"completed"`, `"status":"completed","Status":"failed"`},
		{`"id":"root"`, `"id":"root","id":"other"`},
		{`"id":"root"`, `"id":"root","\u0069d":"other"`},
		{`"id":"root"`, `"id":"root","ID":"other"`},
		{`"session_id":"sess_test"`, `"session_id":"sess_test","session_id":"other"`},
		{`"session_id":"sess_test"`, `"session_id":"sess_test","\u0073ession_id":"other"`},
		{`"session_id":"sess_test"`, `"session_id":"sess_test","Session_id":"other"`},
		{`"turn_id":"root"`, `"turn_id":"root","turn_id":"other"`},
		{`"turn_id":"root"`, `"turn_id":"root","\u0074urn_id":"other"`},
		{`"turn_id":"root"`, `"turn_id":"root","Turn_id":"other"`},
		{`"subagent_id":null`, `"subagent_id":null,"subagent_id":"sub_other"`},
		{`"subagent_id":null`, `"subagent_id":null,"\u0073ubagent_id":"sub_other"`},
		{`"subagent_id":null`, `"subagent_id":null,"Subagent_id":"sub_other"`},
		{`"turn":{"session_id":"sess_test"`, `"turn":{"session_id":"sess_test","session_id":"other"`},
		{`"turn":{"session_id":"sess_test"`, `"turn":{"session_id":"sess_test","\u0073ession_id":"other"`},
		{`"turn":{"session_id":"sess_test"`, `"turn":{"session_id":"sess_test","Session_id":"other"`},
		{`}}`, `},"turn":{}}`},
		{`}}`, `},"\u0074urn":{}}`},
		{`}}`, `},"Turn":{}}`},
	} {
		t.Run(tc.replacement, func(t *testing.T) {
			value := gjson.Parse(strings.Replace(valid, tc.field, tc.replacement, 1))
			require.True(t, gjson.Valid(value.Raw), "ambiguity differs from invalid JSON syntax")
			if AgentsRootTurnTerminal(value, agentsEventsRoute) {
				t.Error("ambiguous event confirmed a root outcome")
			}
			var state AgentsStreamState
			state.Observe(value, agentsEventsRoute)
			require.NotEmpty(t, state.CompletionError(agentsEventsRoute))
			require.False(t, state.completed)
		})
	}
}

func TestAgentsTerminalAmbiguityCannotErasePendingTurns(t *testing.T) {
	var state AgentsStreamState
	state.Observe(agentsTurnEvent("completed", "root", "null"), agentsEventsRoute)
	state.Observe(agentsTextEvent("delta", "ev_pending", "pending", "msg", "unfinished"), agentsEventsRoute)
	child := agentsTurnEvent("completed", "pending", `"sub_child"`).Raw
	ambiguous := strings.Replace(child, `"subagent_id":"sub_child"`, `"subagent_id":"sub_child","subagent_id":null`, 1)
	state.Observe(gjson.Parse(ambiguous), agentsEventsRoute)
	require.Contains(t, state.CompletionError(agentsEventsRoute), "before the agent turn completed")
	require.Empty(t, state.subagentTurns, "an ambiguous terminal event cannot establish delegated ownership")
	state.Observe(agentsTurnEvent("completed", "pending", "null"), agentsEventsRoute)
	require.Empty(t, state.CompletionError(agentsEventsRoute))

	// Later malformed noise cannot revoke an already confirmed outcome for this turn.
	for _, event := range []string{
		strings.Replace(agentsTurnEvent("completed", "root", "null").Raw, `"status":"completed",`, "", 1),
		strings.Replace(agentsTurnEvent("completed", "root", "null").Raw, `"status":"completed"`, `"status":"completed","status":"failed"`, 1),
		strings.Replace(agentsTurnEvent("completed", "root", "null").Raw, `"turn_id":"root"`, `"turn_id":"root","turn_id":"other"`, 1),
	} {
		state.Observe(gjson.Parse(event), agentsEventsRoute)
		require.Empty(t, state.CompletionError(agentsEventsRoute))
	}
}
