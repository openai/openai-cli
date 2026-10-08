package transformers

import (
	"context"
	"strings"

	"github.com/tidwall/gjson"
)

// summarizeAgentsResource retains omission metadata for the readable presenter.
// A zero summary preserves unsupported routes, unknown fields, and shapes.
func summarizeAgentsResource(ctx context.Context, value gjson.Result, route Route) (gjson.Result, bool, error) {
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, false, err
	}
	operation, ok := strings.CutPrefix(route.Operation, "(resource) ")
	resource, method, found := strings.Cut(operation, " > (method) ")
	if !ok || !found || !(route.OutputKind == OutputResponse && (method == "create" || method == "retrieve" || method == "update") ||
		route.OutputKind == OutputPageItem && method == "list") {
		return gjson.Result{}, false, nil
	}
	var object, fields, omitted string
	switch resource {
	case "beta.agents":
		object, fields = "agent", "id name model"
		omitted = "object created_at updated_at instructions tools reasoning text service_tier multi_agent metadata"
	case "beta.agents.sessions":
		object, fields = "agent.session", "id status error required_actions usage"
		omitted = "object created_at last_active_at agent environment metadata vault_ids"
	case "beta.agents.sessions.turns", "beta.agents.sessions.subagents.turns":
		object, fields = "agent.session.turn", "id session_id agent_id subagent_id status error usage"
		omitted = "object created_at started_at completed_at"
	case "beta.agents.sessions.artifacts":
		object, fields = "agent.session.artifact", "id session_id turn_id path size_bytes environment_id"
		omitted = "object created_at"
	case "beta.agents.sessions.traces":
		object, fields = "agent.session.trace", "id session_id created_at"
		omitted = "object otlp"
	default:
		return gjson.Result{}, false, nil
	}
	if !value.IsObject() || value.Get("object").String() != object || value.Get("id").Type != gjson.String || value.Get("id").Str == "" {
		return gjson.Result{}, false, nil
	}
	return summarizeResourceFields(ctx, value, strings.Fields(fields), strings.Fields(omitted))
}
