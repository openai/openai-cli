package custom

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func agentsTerminalStatusEvent(kind, turn, status, subagent string) string {
	field := ""
	if status != "" {
		field = `,"status":` + status
	}
	return fmt.Sprintf(`{"type":"agent.session.turn.%s","session_id":"sess_terminal","turn_id":%q,"turn":{"id":%q,"session_id":"sess_terminal","subagent_id":%s%s}}`, kind, turn, turn, subagent, field)
}

func TestAgentsTerminalStatusPresenterPreservesOutcomeAndBytes(t *testing.T) {
	completed := agentsTerminalStatusEvent("completed", "root", `"completed"`, "null")
	failed := agentsTerminalStatusEvent("failed", "failed_root", `"failed"`, "null")
	const tail = `{"type":"future.usage","retained":"trailing record","integer":9007199254740993}`
	for _, tc := range []struct {
		name   string
		events []string
		err    string
	}{
		{"missing completed status", []string{agentsTerminalStatusEvent("completed", "root", "", "null")}, "before the agent turn completed"},
		{"null status", []string{agentsTerminalStatusEvent("completed", "root", "null", "null")}, "before the agent turn completed"},
		{"number status", []string{agentsTerminalStatusEvent("completed", "root", "0", "null")}, "before the agent turn completed"},
		{"boolean status", []string{agentsTerminalStatusEvent("completed", "root", "true", "null")}, "before the agent turn completed"},
		{"array status", []string{agentsTerminalStatusEvent("completed", "root", `["completed"]`, "null")}, "before the agent turn completed"},
		{"object status", []string{agentsTerminalStatusEvent("completed", "root", `{"value":"completed"}`, "null")}, "before the agent turn completed"},
		{"mismatched status", []string{agentsTerminalStatusEvent("completed", "root", `"failed"`, "null")}, "before the agent turn completed"},
		{"missing failed status", []string{agentsTerminalStatusEvent("failed", "root", "", "null")}, "before the agent turn completed"},
		{"missing cancelled status", []string{agentsTerminalStatusEvent("cancelled", "root", "", "null")}, "before the agent turn completed"},
		{"valid completed", []string{completed}, ""},
		{"valid failed", []string{failed}, "the agent turn failed"},
		{"valid cancelled", []string{agentsTerminalStatusEvent("cancelled", "root", `"cancelled"`, "null")}, "the agent turn was cancelled"},
		{"subagent failed root completed", []string{agentsTerminalStatusEvent("failed", "child", `"failed"`, `"sub_synthetic"`), completed}, ""},
		{"later valid confirmation", []string{agentsTerminalStatusEvent("completed", "root", "", "null"), completed}, ""},
		{"earlier valid failure wins", []string{failed, agentsTerminalStatusEvent("completed", "root", "", "null")}, "the agent turn failed"},
		{"next turn lacks status", []string{completed, agentsTerminalStatusEvent("completed", "next", "", "null")}, "before the agent turn completed"},
		{"duplicate status", []string{strings.Replace(completed, `"status":"completed"`, `"status":"completed","status":"failed"`, 1)}, "before the agent turn completed"},
		{"escaped duplicate status", []string{strings.Replace(completed, `"status":"completed"`, `"status":"completed","\u0073tatus":"failed"`, 1)}, "before the agent turn completed"},
		{"case ambiguous event type", []string{strings.Replace(completed, `"type":"agent.session.turn.completed"`, `"type":"agent.session.turn.completed","Type":"agent.session.turn.failed"`, 1)}, "confirmed agent turn outcome"},
		{"duplicate embedded identity", []string{strings.Replace(completed, `"id":"root"`, `"id":"root","id":"other"`, 1)}, "before the agent turn completed"},
		{"ambiguous subagent keeps pending work", []string{
			completed,
			`{"type":"agent.session.turn.output_text.delta","session_id":"sess_terminal","turn_id":"pending","item_id":"item_pending","delta":"unfinished"}`,
			strings.Replace(agentsTerminalStatusEvent("completed", "pending", `"completed"`, `"sub_child"`), `"subagent_id":"sub_child"`, `"subagent_id":"sub_child","subagent_id":null`, 1),
		}, "before the agent turn completed"},
	} {
		for _, operation := range []string{"(resource) beta.agents.sessions > (method) create", "(resource) beta.agents.sessions.events > (method) stream"} {
			for _, format := range []string{"text", "jsonl", "raw"} {
				t.Run(tc.name+"/"+operation+"/"+format, func(t *testing.T) {
					events := append(append([]string(nil), tc.events...), tail)
					stream := &agentsOwnedStream{}
					for _, event := range events {
						stream.values = append(stream.values, gjson.Parse(event))
					}
					var out, diagnostic bytes.Buffer
					err := ShowJSONIterator(stream, -1, ShowJSONOpts{
						Operation: operation, OutputKind: OutputStreamEvent, Format: format,
						ExplicitFormat: true, Stdout: &out, Stderr: &diagnostic,
					})
					if tc.err == "" {
						require.NoError(t, err)
					} else {
						require.ErrorContains(t, err, tc.err)
					}
					require.Equal(t, len(events)+1, stream.reads)
					require.Equal(t, 1, stream.closes)
					require.Contains(t, out.String(), "trailing record")
					if format != "text" {
						require.Equal(t, strings.Join(events, "\n")+"\n", out.String())
						require.Empty(t, diagnostic.String())
					}
				})
			}
		}
	}
}

func TestAgentsTerminalStatusPreservesNoInputCreationAndTransportErrors(t *testing.T) {
	const created = `{"type":"agent.session.created","session":{"id":"sess_terminal","status":"idle","error":null,"environment":{"id":"env_terminal","type":"self_hosted"}}}`
	for _, tc := range []struct {
		name, terminal string
		ok             bool
	}{
		{"created without turn", "", true},
		{"unconfirmed turn", agentsTerminalStatusEvent("completed", "root", "", "null"), false},
		{"confirmed turn", agentsTerminalStatusEvent("completed", "root", `"completed"`, "null"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			intent := &agentsSessionCreationIntent{}
			intent.withoutInput.Store(true)
			ctx := context.WithValue(t.Context(), agentsSessionCreationKey{}, intent)
			stream := &agentsOwnedStream{values: []gjson.Result{gjson.Parse(created)}}
			if tc.terminal != "" {
				stream.values = append(stream.values, gjson.Parse(tc.terminal))
			}
			var out bytes.Buffer
			err := ShowJSONIterator(stream, -1, ShowJSONOpts{
				Context: ctx, Operation: "(resource) beta.agents.sessions > (method) create",
				OutputKind: OutputStreamEvent, Format: "jsonl", Stdout: &out,
			})
			require.Equal(t, tc.ok, err == nil)
			require.Equal(t, 1, stream.closes)
		})
	}
	transport := errors.New("synthetic transport failure")
	stream := &agentsOwnedStream{readErr: transport, values: []gjson.Result{
		gjson.Parse(agentsTerminalStatusEvent("completed", "root", "", "null")),
	}}
	var out bytes.Buffer
	err := ShowJSONIterator(stream, -1, ShowJSONOpts{
		Operation:  "(resource) beta.agents.sessions.events > (method) stream",
		OutputKind: OutputStreamEvent, Format: "jsonl", Stdout: &out,
	})
	require.ErrorIs(t, err, transport)
	require.Equal(t, 1, stream.closes)
}
