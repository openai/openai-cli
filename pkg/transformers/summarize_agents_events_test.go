package transformers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAgentsLifecycleSummariesKeepStateAndRequiredActions(t *testing.T) {
	output := agentsRender(t,
		gjson.Parse(`{"type":"agent.session.in_progress","event_id":"ev_1","session":{"id":"sess_test","object":"agent.session","status":"in_progress","agent":{"model":"synthetic","instructions":"long instructions"},"environment":{"type":"none"},"created_at":1,"last_active_at":2,"metadata":{"label":"private metadata"},"vault_ids":[],"usage":null,"error":null,"required_actions":[]}}`),
		gjson.Parse(`{"type":"agent.session.requires_action","event_id":"ev_2","session":{"id":"sess_test","object":"agent.session","status":"requires_action","agent":{"instructions":"long instructions"},"environment":{"type":"none"},"created_at":1,"last_active_at":2,"metadata":{},"vault_ids":[],"usage":null,"error":null,"required_actions":[{"type":"function_call","call_id":"call_test","name":"lookup","arguments":{"key":"synthetic"}}]}}`),
		gjson.Parse(`{"type":"agent.session.failed","event_id":"ev_3","session":{"id":"sess_test","object":"agent.session","status":"failed","agent":{"instructions":"long instructions"},"environment":{"type":"none"},"created_at":1,"last_active_at":2,"metadata":{},"vault_ids":[],"usage":null,"error":"synthetic session error","required_actions":[]}}`))
	require.Contains(t, output, "Session:")
	require.Contains(t, output, "sess_test")
	require.Contains(t, output, "Status: in_progress")
	require.Contains(t, output, "Status: requires_action")
	require.Contains(t, output, "call_test")
	require.Contains(t, output, "lookup")
	require.Contains(t, output, "synthetic session error")
	require.NotContains(t, output, "long instructions")
	require.NotContains(t, output, "private metadata")
	require.NotContains(t, output, "Created at")
	require.NotContains(t, output, "--format")
}

func TestAgentsTurnSummaryKeepsFailureAndUsage(t *testing.T) {
	output := agentsRender(t, gjson.Parse(`{"type":"agent.session.turn.failed","event_id":"ev_1","session_id":"sess_test","turn_id":"turn_test","turn":{"id":"turn_test","object":"agent.session.turn","session_id":"sess_test","agent_id":"agent_test","subagent_id":null,"status":"failed","created_at":1,"started_at":2,"completed_at":3,"error":{"code":"synthetic_failure","message":"synthetic failure detail"},"usage":{"total_tokens":9007199254740993}},"usage":{"total_tokens":9007199254740993}}`))
	require.Contains(t, output, "Turn:")
	require.Contains(t, output, "turn_test")
	require.Contains(t, output, "Status: failed")
	require.Contains(t, output, "synthetic_failure")
	require.Contains(t, output, "9007199254740993")
	require.NotContains(t, output, "Created at")
	require.NotContains(t, output, "Completed at")
}

func TestAgentsToolSummariesOmitKnownArgumentsAndTranscripts(t *testing.T) {
	output := agentsRender(t,
		gjson.Parse(`{"type":"agent.session.turn.item.added","event_id":"ev_fn","session_id":"sess_test","turn_id":"turn_test","output_index":0,"item":{"id":"tool_function","type":"function_call","name":"lookup","call_id":"call_test","status":"in_progress","turn_id":"turn_test","arguments":{"long":"tool arguments"}}}`),
		gjson.Parse(`{"type":"agent.session.turn.item.done","event_id":"ev_mcp","session_id":"sess_test","turn_id":"turn_test","output_index":1,"item":{"id":"tool_mcp","type":"mcp_call","name":"read","server_label":"synthetic_server","status":"failed","turn_id":"turn_test","arguments":{"long":"tool arguments"},"output":"long tool output","error":{"code":"synthetic_tool_failure","message":"synthetic tool detail"}}}`),
		gjson.Parse(`{"type":"agent.session.turn.item.done","event_id":"ev_shell","session_id":"sess_test","turn_id":"turn_test","output_index":2,"item":{"id":"tool_shell","type":"command_execution","status":"failed","turn_id":"turn_test","command":"synthetic command argument","cwd":"/workspace","duration_ms":123,"exit_code":7,"output":"long shell output"}}`),
		gjson.Parse(`{"type":"agent.output.command_execution_output.delta","event_id":"ev_delta","session_id":"sess_test","turn_id":"turn_test","item_id":"tool_shell","output_index":2,"delta":"more shell output"}`))
	require.Contains(t, output, "function_call")
	require.Contains(t, output, "lookup")
	require.Contains(t, output, "call_test")
	require.Contains(t, output, "mcp_call")
	require.Contains(t, output, "synthetic_tool_failure")
	require.Contains(t, output, "synthetic tool detail")
	require.Contains(t, output, "command_execution")
	require.Contains(t, output, "Exit code: 7")
	for _, omitted := range []string{"tool arguments", "long tool output", "long shell output", "more shell output", "synthetic command argument"} {
		require.NotContains(t, output, omitted)
	}
	require.NotContains(t, output, "--format")
}

func TestAgentsProjectorReportsOmissionsWithoutDiagnosticText(t *testing.T) {
	var projector AgentsStreamProjector
	require.False(t, projector.HasOmissions())
	_, projected, err := projector.Project(context.Background(), gjson.Parse(`{"type":"future.event","data":"unchanged"}`), agentsEventsRoute)
	require.NoError(t, err)
	require.False(t, projected)
	require.False(t, projector.HasOmissions())
	value := gjson.Parse(`{"type":"agent.output.command_execution_output.delta","event_id":"ev_test","session_id":"sess_test","turn_id":"turn_test","item_id":"item_test","output_index":0,"delta":"tool transcript"}`)
	event, projected, err := projector.Project(context.Background(), value, agentsEventsRoute)
	require.NoError(t, err)
	require.True(t, projected)
	require.True(t, projector.HasOmissions())
	require.Empty(t, event.Parts, "CLI guidance must not become result text")
	require.False(t, event.Details.Exists())
	_, _, err = projector.Project(context.Background(), gjson.Parse(`{"type":"future.event"}`), agentsEventsRoute)
	require.NoError(t, err)
	require.True(t, projector.HasOmissions(), "later events must not erase earlier omissions")
}

func TestAgentsSummariesPreserveFutureResourceAndEventFields(t *testing.T) {
	output := agentsRender(t,
		gjson.Parse(`{"type":"agent.session.idle","event_id":"ev_session","session":{"id":"sess_test","status":"idle","future_resource":{"large":9007199254740993}}}`),
		gjson.Parse(`{"type":"agent.session.turn.item.done","event_id":"ev_tool","future_event":"keep event field","item":{"id":"tool_test","type":"function_call","status":"completed","name":"lookup","call_id":"call_test","turn_id":"turn_test","arguments":{}}}`),
		gjson.Parse(`{"type":"agent.output.command_execution_output.delta","event_id":"ev_delta","session_id":"sess_test","turn_id":"turn_test","item_id":"tool_test","output_index":0,"delta":"tool output","future_event":"keep delta field"}`))
	require.Contains(t, output, "9007199254740993")
	require.Contains(t, output, "keep event field")
	require.Contains(t, output, "keep delta field")
	require.Contains(t, output, "tool output")
}
