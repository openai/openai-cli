package transformers

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const agentsCreationAck = `{"type":"agent.session.created","session":{"id":"sess_test","status":"idle","error":null,"environment":{"id":"env_test","type":"self_hosted"}}}`

func TestAgentsNoInputCreationAcknowledgementIsRequestAndRouteScoped(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, route := range []Route{agentsCreateRoute, agentsEventsRoute} {
			state := AgentsStreamState{AllowNoInputSessionCreation: enabled}
			value := gjson.Parse(agentsCreationAck)
			state.Observe(value, route)
			require.Equal(t, enabled && route == agentsCreateRoute, state.CompletionError(route) == "")
			require.False(t, state.completed, "creation acknowledgement must not claim root-turn completion")
			require.Equal(t, agentsCreationAck, value.Raw)
		}
	}
}

func TestAgentsNoInputCreationRequiresConsistentSemanticEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events []string
		ok     bool
	}{
		{"created only", []string{agentsCreationAck}, true},
		{"error absent", []string{strings.Replace(agentsCreationAck, `"error":null,`, "", 1)}, true},
		{"duplicate acknowledgement", []string{agentsCreationAck, agentsCreationAck}, true},
		{"outer identity matches", []string{strings.Replace(agentsCreationAck, `"session":`, `"session_id":"sess_test","session":`, 1)}, true},
		{"idle follows", []string{agentsCreationAck, `{"type":"agent.session.idle","session_id":"sess_test"}`}, true},
		{"unknown follows", []string{agentsCreationAck, `{"type":"future.event","session_id":"sess_test","future":true}`}, true},
		{"empty", nil, false},
		{"idle only", []string{`{"type":"agent.session.idle","session_id":"sess_test"}`}, false},
		{"unknown only", []string{`{"type":"future.event"}`}, false},
		{"session missing", []string{`{"type":"agent.session.created"}`}, false},
		{"session ID empty", []string{strings.Replace(agentsCreationAck, `"sess_test"`, `""`, 1)}, false},
		{"session ID null", []string{strings.Replace(agentsCreationAck, `"sess_test"`, `null`, 1)}, false},
		{"environment ID empty", []string{strings.Replace(agentsCreationAck, `"env_test"`, `""`, 1)}, false},
		{"environment missing", []string{`{"type":"agent.session.created","session":{"id":"sess_test","status":"idle","error":null}}`}, false},
		{"hosted environment", []string{strings.Replace(agentsCreationAck, "self_hosted", "openai_hosted", 1)}, false},
		{"nonnull error", []string{strings.Replace(agentsCreationAck, `"error":null`, `"error":"synthetic failure"`, 1)}, false},
		{"empty nonnull error", []string{strings.Replace(agentsCreationAck, `"error":null`, `"error":""`, 1)}, false},
		{"status missing", []string{strings.Replace(agentsCreationAck, `"status":"idle",`, "", 1)}, false},
		{"status active", []string{strings.Replace(agentsCreationAck, `"status":"idle"`, `"status":"in_progress"`, 1)}, false},
		{"duplicate event type", []string{strings.Replace(agentsCreationAck, `"type":"agent.session.created"`, `"type":"agent.session.created","type":"agent.session.failed"`, 1)}, false},
		{"ambiguous event type", []string{strings.Replace(agentsCreationAck, `"type":"agent.session.created"`, `"type":"agent.session.created","Type":"agent.session.in_progress"`, 1)}, false},
		{"duplicate status", []string{strings.Replace(agentsCreationAck, `"status":"idle"`, `"status":"idle","status":"in_progress"`, 1)}, false},
		{"ambiguous status", []string{strings.Replace(agentsCreationAck, `"status":"idle"`, `"status":"idle","Status":"idle"`, 1)}, false},
		{"duplicate environment type", []string{strings.Replace(agentsCreationAck, `"type":"self_hosted"`, `"type":"self_hosted","type":"self_hosted"`, 1)}, false},
		{"malformed later record", []string{agentsCreationAck, `{"type":`}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := AgentsStreamState{AllowNoInputSessionCreation: true}
			for _, event := range tc.events {
				state.Observe(gjson.Parse(event), agentsCreateRoute)
			}
			require.Equal(t, tc.ok, state.CompletionError(agentsCreateRoute) == "")
			require.False(t, state.completed)
		})
	}
}

func TestAgentsNoInputCreationWorkAndFailuresKeepNormalOutcomeRules(t *testing.T) {
	for _, event := range []string{
		`{"type":"agent.session.in_progress"}`,
		`{"type":"agent.session.requires_action"}`,
		`{"type":"agent.session.idle","session":{"id":"sess_test","status":"requires_action"}}`,
		`{"type":"agent.session.idle","session":{"id":"sess_test","status":"idle","required_actions":[{"type":"function_call"}]}}`,
		`{"type":"agent.session.turn.created"}`,
		`{"type":"agent.session.turn.future"}`,
		`{"type":"agent.session.turn.output_text.delta","session_id":"sess_test","turn_id":"orphan","delta":"synthetic"}`,
		`{"type":"agent.output.command_execution_output.delta"}`,
		`{"type":"agent.session.subagent.created"}`,
		`{"type":"agent.session.failed"}`,
		`{"type":"agent.session.environment.failed"}`,
		`{"type":"error","error":{"message":"synthetic failure"}}`,
	} {
		t.Run(event, func(t *testing.T) {
			for _, events := range [][]string{{agentsCreationAck, event}, {event, agentsCreationAck}} {
				state := AgentsStreamState{AllowNoInputSessionCreation: true}
				for _, value := range events {
					state.Observe(gjson.Parse(value), agentsCreateRoute)
				}
				require.NotEmpty(t, state.CompletionError(agentsCreateRoute))
			}
		})
	}
	state := AgentsStreamState{AllowNoInputSessionCreation: true}
	state.Observe(gjson.Parse(agentsCreationAck), agentsCreateRoute)
	state.Observe(agentsTurnEvent("created", "turn_test", "null"), agentsCreateRoute)
	require.NotEmpty(t, state.CompletionError(agentsCreateRoute))
	state.Observe(agentsTurnEvent("completed", "turn_test", "null"), agentsCreateRoute)
	require.Empty(t, state.CompletionError(agentsCreateRoute), "normal root completion remains available after observed work")
	state.Observe(agentsTurnEvent("created", "next_turn", "null"), agentsCreateRoute)
	require.NotEmpty(t, state.CompletionError(agentsCreateRoute))
}

func TestAgentsNoInputCreationConflictingIdentitiesRemainErrors(t *testing.T) {
	for _, conflict := range []string{
		strings.Replace(agentsCreationAck, "sess_test", "sess_other", 1),
		strings.Replace(agentsCreationAck, "env_test", "env_other", 1),
		strings.Replace(agentsCreationAck, `"session":`, `"session_id":"sess_other","session":`, 1),
		`{"type":"agent.session.idle","session_id":"sess_other"}`,
		`{"type":"future.event","session_id":"sess_other"}`,
	} {
		t.Run(conflict, func(t *testing.T) {
			state := AgentsStreamState{AllowNoInputSessionCreation: true}
			state.Observe(gjson.Parse(agentsCreationAck), agentsCreateRoute)
			state.Observe(gjson.Parse(conflict), agentsCreateRoute)
			require.Contains(t, state.CompletionError(agentsCreateRoute), "inconsistent agent session identities")
			state.Observe(agentsTurnEvent("completed", "turn_test", "null"), agentsCreateRoute)
			require.Contains(t, state.CompletionError(agentsCreateRoute), "inconsistent agent session identities")
			state.Observe(agentsTurnEvent("failed", "failed_turn", "null"), agentsCreateRoute)
			require.Equal(t, "the agent turn failed", state.CompletionError(agentsCreateRoute), "explicit failure has precedence")
		})
	}
}

func TestAgentsNoInputCreationLaterEnvironmentConsistency(t *testing.T) {
	for _, environment := range []string{
		`{"id":"env_other","type":"self_hosted"}`,
		`{"id":"env_test","type":"openai_hosted"}`,
		`{"id":"env_test","type":"self_hosted","type":"openai_hosted"}`,
	} {
		t.Run(environment, func(t *testing.T) {
			state := AgentsStreamState{AllowNoInputSessionCreation: true}
			state.Observe(gjson.Parse(agentsCreationAck), agentsCreateRoute)
			state.Observe(gjson.Parse(`{"type":"agent.session.idle","session":{"id":"sess_test","status":"idle","error":null,"environment":`+environment+`}}`), agentsCreateRoute)
			require.NotEmpty(t, state.CompletionError(agentsCreateRoute))
		})
	}
	state := AgentsStreamState{AllowNoInputSessionCreation: true}
	state.Observe(gjson.Parse(agentsCreationAck), agentsCreateRoute)
	state.Observe(gjson.Parse(strings.Replace(agentsCreationAck, "agent.session.created", "agent.session.idle", 1)), agentsCreateRoute)
	require.Empty(t, state.CompletionError(agentsCreateRoute))
}

func TestAgentsNoInputCreationInputEventsDisableAcknowledgement(t *testing.T) {
	for _, kind := range []string{"message", "cancel", "tool_result", "computer_use_approval_request_result", "future_input"} {
		t.Run(kind, func(t *testing.T) {
			state := AgentsStreamState{AllowNoInputSessionCreation: true}
			state.Observe(gjson.Parse(agentsCreationAck), agentsCreateRoute)
			state.Observe(gjson.Parse(`{"type":"agent.session.input.`+kind+`","session_id":"sess_test"}`), agentsCreateRoute)
			require.NotEmpty(t, state.CompletionError(agentsCreateRoute))
		})
	}
}

func TestAgentsNoInputCreationEnvironmentEventsRemainConsistent(t *testing.T) {
	connected := `{"type":"agent.session.environment.connected","session_id":"sess_test","turn_id":null,"environment":{"id":"env_test","type":"self_hosted","status":"connected","error":null}}`
	reset := `{"type":"agent.session.environment.reset","session_id":"sess_test","turn_id":null,"environment_id":"env_test","reset_count":1}`
	for _, tc := range []struct {
		name, event string
		ok          bool
	}{
		{"matching connected", connected, true},
		{"different environment", strings.Replace(connected, "env_test", "env_other", 1), false},
		{"different type", strings.Replace(connected, "self_hosted", "openai_hosted", 1), false},
		{"null environment", strings.Replace(connected, `"id":"env_test"`, `"id":null`, 1), false},
		{"duplicate environment", strings.TrimSuffix(connected, "}") + `,"environment":{"id":"env_other","type":"self_hosted"}}`, false},
		{"failed state", strings.Replace(connected, `"status":"connected"`, `"status":"failed"`, 1), false},
		{"reported error", strings.Replace(connected, `"error":null`, `"error":{"type":"synthetic","code":"synthetic","message":"synthetic"}`, 1), false},
		{"turn activity", strings.Replace(connected, `"turn_id":null`, `"turn_id":"turn_active"`, 1), false},
		{"matching reset", reset, true},
		{"different reset environment", strings.Replace(reset, "env_test", "env_other", 1), false},
		{"null reset environment", strings.Replace(reset, `"environment_id":"env_test"`, `"environment_id":null`, 1), false},
		{"duplicate reset environment", strings.Replace(reset, `"environment_id":"env_test"`, `"environment_id":"env_test","environment_id":"env_other"`, 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, events := range [][]string{{agentsCreationAck, tc.event}, {tc.event, agentsCreationAck}} {
				state := AgentsStreamState{AllowNoInputSessionCreation: true}
				for _, event := range events {
					state.Observe(gjson.Parse(event), agentsCreateRoute)
				}
				require.Equal(t, tc.ok, state.CompletionError(agentsCreateRoute) == "")
			}
		})
	}
}
