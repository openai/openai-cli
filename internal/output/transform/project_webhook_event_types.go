package transform

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"
)

// ProjectWebhookEventTypes groups the supported catalog for CLI discovery.
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
		if strings.HasPrefix(item.Str, "fine_tuning.") {
			return true
		}
		group := "other"
		for _, candidate := range []struct{ prefix, group string }{
			{"response.", "background_responses"}, {"batch.", "batches"}, {"eval.", "evaluations"},
			{"realtime.", "realtime"}, {"video.", "videos"},
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
		if value.Get("data.#").Int() > 0 {
			return gjson.Parse(`"The available webhook events are not supported by the CLI."`), nil
		}
		return gjson.Parse(`"No webhook events are available for this project."`), nil
	}
	encoded, err := json.Marshal(groups)
	if err != nil {
		return gjson.Result{}, err
	}
	return gjson.ParseBytes(encoded), ctx.Err()
}

// WebhookEventTypeRows reuses the readable catalog policy. Each pager record
// keeps the event name and its category, including events in the "other" group.
// An unexpected catalog is returned intact for labeled output.
func WebhookEventTypeRows(ctx context.Context, value gjson.Result) (rows []gjson.Result, notice string, full bool, err error) {
	catalog, err := ProjectWebhookEventTypes(ctx, value)
	if err != nil {
		return nil, "", false, err
	}
	if catalog.Raw == value.Raw {
		return []gjson.Result{value}, "The API returned an unexpected event catalog.", true, nil
	}
	if catalog.Type == gjson.String {
		return nil, catalog.Str, false, nil
	}
	catalog.ForEach(func(category, events gjson.Result) bool {
		// The projection emits JSON-encoded category keys and string values.
		events.ForEach(func(_, event gjson.Result) bool {
			if ctx.Err() != nil {
				return false
			}
			rows = append(rows, gjson.Parse(`{"event":`+event.Raw+`,"category":`+category.Raw+`}`))
			return true
		})
		return ctx.Err() == nil
	})
	return rows, "", false, ctx.Err()
}
