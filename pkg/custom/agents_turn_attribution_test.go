package custom

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAgentsNonterminalAttributionPresenter(t *testing.T) {
	completed := agentsTerminalStatusEvent("completed", "root", `"completed"`, "null")
	failed := agentsTerminalStatusEvent("failed", "root", `"failed"`, "null")
	const pending = `{"type":"agent.session.turn.output_text.delta","event_id":"pending_delta","session_id":"sess_terminal","turn_id":"pending","item_id":"msg_pending","content_index":0,"delta":"unfinished"}`
	child := agentsTerminalStatusEvent("created", "pending", `"queued"`, `"sub_child"`)
	ambiguous := strings.TrimSuffix(child, "}") + `,"turn":{"id":"pending","session_id":"sess_terminal","subagent_id":null,"status":"queued"}}`
	const tail = `{"type":"future.usage","marker":"retained tail","integer":9007199254740993}`
	for _, tc := range []struct {
		name   string
		events []string
		err    string
	}{
		{"duplicate turn keeps pending", []string{completed, pending, ambiguous}, "before the agent turn completed"},
		{"escaped turn keeps pending", []string{completed, pending, strings.Replace(ambiguous, `},"turn":`, `},"\u0074urn":`, 1)}, "before the agent turn completed"},
		{"duplicate nested role keeps pending", []string{completed, pending, strings.Replace(child, `"subagent_id":"sub_child"`, `"subagent_id":"sub_child","subagent_id":null`, 1)}, "before the agent turn completed"},
		{"later child attribution", []string{completed, pending, ambiguous, child}, ""},
		{"later root completion", []string{completed, pending, ambiguous, agentsTerminalStatusEvent("completed", "pending", `"completed"`, "null")}, ""},
		{"confirmed root survives", []string{completed, strings.ReplaceAll(ambiguous, "pending", "root")}, ""},
		{"known child survives", []string{completed, child, strings.Replace(agentsTerminalStatusEvent("in_progress", "pending", `"in_progress"`, "null"), `"subagent_id":null`, `"subagent_id":null,"subagent_id":"sub_child"`, 1)}, ""},
		{"earlier failure wins", []string{failed, pending, ambiguous}, "the agent turn failed"},
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
					require.Contains(t, out.String(), "retained tail")
					if format != "text" {
						require.Equal(t, strings.Join(events, "\n")+"\n", out.String())
						require.Empty(t, diagnostic.String())
					}
				})
			}
		}
	}
}
