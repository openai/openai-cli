package transformers

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"
)

// ProjectWebhookEventTypes groups the complete known catalog for discovery.
// Unknown response fields or item types retain their full generic representation.
func ProjectWebhookEventTypes(ctx context.Context, value gjson.Result) (gjson.Result, error) {
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	if !value.IsObject() || !gjson.Valid(value.Raw) || value.Get("object").Str != "list" || !value.Get("data").IsArray() {
		return value, ctx.Err()
	}
	seen := map[string]bool{}
	valid := true
	value.ForEach(func(key, _ gjson.Result) bool {
		if ctx.Err() != nil || seen[key.Str] || key.Str != "object" && key.Str != "data" {
			valid = false
			return false
		}
		seen[key.Str] = true
		return true
	})
	groups := map[string][]string{}
	value.Get("data").ForEach(func(_, item gjson.Result) bool {
		if ctx.Err() != nil || item.Type != gjson.String {
			valid = false
			return false
		}
		group := "other"
		for _, candidate := range []struct{ prefix, group string }{
			{"response.", "background_responses"}, {"batch.", "batches"}, {"eval.", "evaluations"},
			{"fine_tuning.", "fine_tuning"}, {"realtime.", "realtime"}, {"video.", "videos"},
			{"agent.session.", "agent_sessions"}, {"agent.environment.", "agent_environments"}, {"safety.", "safety"},
		} {
			if strings.HasPrefix(item.Str, candidate.prefix) {
				group = candidate.group
				break
			}
		}
		groups[group] = append(groups[group], item.Str)
		return true
	})
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	if !valid {
		return value, nil
	}
	if len(groups) == 0 {
		return gjson.Parse(`"No webhook events are available for this project."`), nil
	}
	encoded, err := json.Marshal(groups)
	if err != nil {
		return gjson.Result{}, err
	}
	return gjson.ParseBytes(encoded), ctx.Err()
}
