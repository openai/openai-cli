package transformers

import (
	"context"
	"strings"

	"github.com/tidwall/gjson"
)

// SelectAgentsTransformer returns a readable resource projection, or nil for
// unrelated routes. The presentation boundary excludes explicit machine formats.
func SelectAgentsTransformer(route Route) Transformer {
	if route.OutputKind != OutputResponse && route.OutputKind != OutputPageItem {
		return nil
	}
	operation, ok := strings.CutPrefix(route.Operation, "(resource) ")
	resource, method, found := strings.Cut(operation, " > (method) ")
	if !ok || !found || method != "create" && method != "retrieve" && method != "update" && method != "list" {
		return nil
	}
	var object, fields, omitted string
	switch resource {
	case "beta.agents":
		object, fields = "agent", "id name model"
		omitted = "object created_at instructions tools reasoning text service_tier multi_agent metadata"
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
		return nil
	}
	return func(ctx context.Context, value gjson.Result) (gjson.Result, error) {
		if err := ctx.Err(); err != nil {
			return gjson.Result{}, err
		}
		if !value.IsObject() || value.Get("object").String() != object || value.Get("id").Type != gjson.String || value.Get("id").Str == "" {
			return value, nil
		}
		result, _, err := summarizeResourceFields(ctx, value, strings.Fields(fields), strings.Fields(omitted))
		if err != nil {
			return gjson.Result{}, err
		}
		if !result.Exists() {
			return value, nil
		}
		return result, nil
	}
}
