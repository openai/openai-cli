package transform

import (
	"context"
	"strings"

	"github.com/tidwall/gjson"
)

// IsResourceListOperation recognizes canonical list and list_* method names.
// Callers also check OutputPageItem to exclude raw envelopes and streams.
func IsResourceListOperation(operation string) bool {
	path, ok := strings.CutPrefix(operation, "(resource) ")
	resource, method, found := strings.Cut(path, " > (method) ")
	return ok && found && resource != "" && !strings.ContainsAny(resource+method, " >") &&
		(method == "list" || strings.HasPrefix(method, "list_") && len(method) > len("list_"))
}

// Project the two compact schemas whose pagers receive raw page items.
func projectCompactListTable(ctx context.Context, operation string, items []gjson.Result) (ListTableProjection, error) {
	var object string
	var fields, headers []string
	omitted := []string{"object", "created_at", "updated_at"}
	switch operation {
	case "(resource) webhooks > (method) list":
		object = "webhook_endpoint"
		fields = []string{"id", "name", "event_types"}
		headers = []string{"ID", "NAME", "EVENTS"}
		omitted = append(omitted, "url", "signing_secret_hint")
	case "(resource) skills > (method) list":
		object = "skill"
		fields = []string{"id", "name", "default_version", "latest_version"}
		headers = []string{"ID", "NAME", "DEFAULT VERSION", "LATEST VERSION"}
		omitted = append(omitted, "description")
	default:
		return projectDynamicListTable(ctx, operation, items)
	}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		values, err := knownResourceFields(ctx, item, fields, omitted)
		if err != nil {
			return ListTableProjection{}, err
		}
		if values["id"].Str == "" || values["object"].Str != object {
			return ListTableProjection{}, nil
		}
		// Malformed metadata may carry useful details. Keep it in labeled output.
		for _, field := range omitted {
			value := values[field]
			if value.Type == gjson.Null {
				continue
			}
			want := gjson.String
			if field == "created_at" || field == "updated_at" {
				want = gjson.Number
			}
			if value.Type != want {
				return ListTableProjection{}, nil
			}
		}
		row := make([]string, len(fields))
		for col, field := range fields {
			value := values[field]
			cell, ok := value.Str, value.Type == gjson.String
			if field == "event_types" {
				cell, ok = listWebhookEvents(ctx, value)
			}
			if err := ctx.Err(); err != nil {
				return ListTableProjection{}, err
			}
			if !ok {
				return ListTableProjection{}, nil
			}
			row[col] = cell
		}
		rows = append(rows, row)
	}
	if err := ctx.Err(); err != nil {
		return ListTableProjection{}, err
	}
	return ListTableProjection{Headers: headers, Rows: rows, FullValues: true}, nil
}

func listWebhookEvents(ctx context.Context, value gjson.Result) (string, bool) {
	if !value.IsArray() {
		return "", false
	}
	var events strings.Builder
	valid := true
	value.ForEach(func(_, event gjson.Result) bool {
		valid = event.Type == gjson.String && event.Str != ""
		if !valid || ctx.Err() != nil {
			return false
		}
		if events.Len() > 0 {
			events.WriteString(", ")
		}
		events.WriteString(event.Str)
		return true
	})
	if events.Len() == 0 {
		return "-", valid
	}
	return events.String(), valid
}
