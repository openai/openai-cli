package transformers

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAgentsLifecycleAmbiguousEnvelopeKeepsCompleteEvent(t *testing.T) {
	for _, test := range []struct{ kind, slot string }{
		{"agent.session.idle", "session"},
		{"agent.session.turn.completed", "turn"},
		{"agent.session.environment.ready", "environment"},
		{"agent.session.subagent.active", "subagent"},
	} {
		base := `{"type":"` + test.kind + `","event_id":"evt_first","output_index":0,"session_id":"sess_test","turn_id":"turn_1","` + test.slot + `":{"id":"first_id","status":"completed"}`
		for _, extra := range []string{
			`,"` + test.slot + `":{"id":"second_id","status":"future_status"}}`,
			`,"event_id":"evt_second"}`,
			`,"output_index":9007199254740993}`,
			`,"type":"future_type"}`,
			`,"session_id":"sess_other"}`,
			`,"turn_id":"turn_other"}`,
		} {
			t.Run(test.slot+"/"+extra, func(t *testing.T) {
				value := gjson.Parse(base + extra)
				event, projected, err := new(AgentsStreamProjector).Project(t.Context(), value, agentsEventsRoute)
				require.NoError(t, err)
				if projected {
					require.Equal(t, value.Raw, event.Details.Raw)
				}
			})
		}
	}
}

func TestAgentsAssistantUnknownDuplicateFieldsRemainVisible(t *testing.T) {
	value := agentsAssistantSnapshot(`"completed"`, `[{"type":"output_text","text":"Answer","future":null,"future":9007199254740993}]`)
	raw := strings.TrimSuffix(value.Raw, "}}") + `,"future":null,"future":false},"future":null,"future":"outer"}`
	event, projected, err := new(AgentsStreamProjector).Project(t.Context(), gjson.Parse(raw), agentsEventsRoute)
	require.NoError(t, err)
	require.True(t, projected)
	require.Equal(t, 6, strings.Count(event.Details.Raw, `"future"`))
	require.Contains(t, event.Details.Raw, "9007199254740993")
}
