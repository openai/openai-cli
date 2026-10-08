package transformers

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

var agentsItemsListRoute = Route{"(resource) beta.agents.sessions.items > (method) list", OutputPageItem}

func TestAgentsPersistedImageProjectionPreservesSourceSpans(t *testing.T) {
	raw := `{"type":"message","id":null,"turn_id":"turn_test","role":"user","status":"completed","phase":null,"future":9007199254740993,"content":[{"type":"input_text","text":"before"},{"type":"input_image","image_url":"data:image/png;base64,QUJDRA%3D%3D","future":null},{"type":"input_image","image_url":"data:image/png;base64,invalid!"},{"type":"future_image","image_url":"data:image/png;base64,RlVUVVJF"},{"type":"input_image","image_url":"data:image/webp;base64,QUJDRA=="},{"type":"input_text","text":"after"}]}`
	for _, value := range []gjson.Result{gjson.Parse(raw), gjson.Get(`{"nested":`+raw+`}`, "nested")} {
		summary, hidden, err := SummarizeResource(t.Context(), value, agentsItemsListRoute)
		require.NoError(t, err)
		require.True(t, hidden)
		require.Equal(t, "(image/png; 8 base64 characters)", summary.Get("content.1.image_url").Str)
		require.Equal(t, "(image/webp; 8 base64 characters)", summary.Get("content.4.image_url").Str)
		for _, field := range []string{"id", "turn_id", "role", "status", "phase", "future", "content.0", "content.1.future", "content.2", "content.3", "content.5"} {
			require.Equal(t, value.Get(field).Raw, summary.Get(field).Raw, field)
		}
		require.Equal(t, raw, value.Raw)
	}
}

func TestAgentsPersistedImageProjectionLeavesOtherDataIntact(t *testing.T) {
	for _, raw := range []string{
		`{"type":"message","role":"assistant","content":[{"type":"input_image","image_url":"data:image/png;base64,QUJDRA=="}]}`,
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"data:image/png;base64,QUJDRA=="}]}`,
		`{"type":"function_call_output","output":"data:image/png;base64,QUJDRA=="}`,
		`{"type":"function_call_output","output":null}`,
		`{"type":"computer_use_call","output":{"type":"computer_screenshot","image_url":"data:image/png;base64,QUJDRA=="}}`,
		`{"type":"computer_use_call","output":{"type":"future_screenshot","image_url":"data:image/jpeg;base64,QUJDRA=="}}`,
		`{"type":"computer_use_call","output":{"type":"computer_screenshot","image_url":"https://synthetic.invalid/image.jpg"}}`,
		`{"type":"future_item","content":[{"type":"input_image","image_url":"data:image/png;base64,QUJDRA=="}]}`,
		`{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,QUJDRA=="}]`,
	} {
		summary, hidden, err := SummarizeResource(t.Context(), gjson.Parse(raw), agentsItemsListRoute)
		require.NoError(t, err)
		require.False(t, hidden)
		if summary.Exists() {
			require.Equal(t, raw, summary.Raw)
		}
	}
	value := gjson.Parse(`{"type":"function_call_output","output":[{"type":"input_image","image_url":"data:image/png;base64,QUJDRA=="}]}`)
	for _, route := range []Route{
		{"(resource) files > (method) list", OutputPageItem},
		{"(resource) beta.agents.sessions.items > (method) retrieve", OutputResponse},
		{"(resource) beta.agents.sessions.items > (method) list", OutputResponse},
		{"(resource) beta.agents.sessions.items > (method) list", OutputStreamEvent},
	} {
		summary, hidden, err := SummarizeResource(t.Context(), value, route)
		require.NoError(t, err)
		require.False(t, summary.Exists())
		require.False(t, hidden)
	}
}

func TestAgentsPersistedImageProjectionCancelsDuringScan(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	controlled := &cancelSummaryContext{Context: ctx, cancel: cancel, after: 8}
	value := gjson.Parse(`{"type":"function_call_output","output":[{"type":"input_image","image_url":` + strconv.Quote("data:image/png;base64,"+strings.Repeat("QUJD", 65536)) + `}]}`)
	summary, hidden, err := SummarizeResource(controlled, value, agentsItemsListRoute)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, hidden)
	require.False(t, summary.Exists())
}

func TestAgentsSubagentResourceProjectionPreservesUnknownShapes(t *testing.T) {
	route := Route{"(resource) beta.agents.sessions.subagents > (method) retrieve", OutputResponse}
	for _, raw := range []string{
		`{"id":"sub_test","object":"agent.session.subagent","status":"active","instructions":null,"future":null}`,
		`{"id":"sub_test","object":"agent.session.subagent","status":"active","status":"closed","instructions":null}`,
		`{"id":"sub_test","object":"future_subagent","status":"active","instructions":null}`,
		`{"id":null,"object":"agent.session.subagent","status":"active","instructions":null}`,
	} {
		summary, hidden, err := SummarizeResource(t.Context(), gjson.Parse(raw), route)
		require.NoError(t, err)
		require.False(t, summary.Exists())
		require.False(t, hidden)
	}
	value := gjson.Parse(`{"id":"sub_test","object":"agent.session.subagent","session_id":"sess_test","name":null,"instructions":null,"parent_agent_id":"parent_test","status":"active","opened_at":1,"closed_at":null}`)
	summary, hidden, err := SummarizeResource(t.Context(), value, route)
	require.NoError(t, err)
	require.True(t, hidden)
	require.Equal(t, `{"id":"sub_test","session_id":"sess_test","status":"active","parent_agent_id":"parent_test"}`, summary.Raw)
}
