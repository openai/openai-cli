package transformers

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

// ProjectLifecycleReceipt contains only values confirmed by a project response.
type ProjectLifecycleReceipt struct {
	ID      string
	Action  string
	Details gjson.Result
}

// ProjectLifecycle selects successful lifecycle responses for readable receipts.
// Unfamiliar, ambiguous, and incomplete responses retain the full output path.
func ProjectLifecycle(ctx context.Context, value gjson.Result, route Route) (ProjectLifecycleReceipt, bool, error) {
	var receipt ProjectLifecycleReceipt
	if err := ctx.Err(); err != nil {
		return receipt, false, err
	}
	if route.OutputKind != OutputResponse {
		return receipt, false, nil
	}
	status := "active"
	switch route.Operation {
	case "(resource) admin.organization.projects > (method) create":
		receipt.Action = "created"
	case "(resource) admin.organization.projects > (method) update":
		receipt.Action = "updated"
	case "(resource) admin.organization.projects > (method) archive":
		receipt.Action, status = "archived", "archived"
	default:
		return receipt, false, nil
	}
	if !value.IsObject() || !gjson.Valid(value.Raw) || value.Get("object").Str != "organization.project" ||
		value.Get("status").Str != status {
		return receipt, false, ctx.Err()
	}
	id := value.Get("id")
	if id.Type != gjson.String || strings.TrimSpace(id.Str) == "" || !utf8.ValidString(id.Str) {
		return receipt, false, ctx.Err()
	}
	seen := map[string]bool{}
	var details strings.Builder
	details.WriteByte('{')
	valid, first := true, true
	value.ForEach(func(key, field gjson.Result) bool {
		if ctx.Err() != nil {
			return false
		}
		if seen[key.Str] {
			valid = false
			return false
		}
		seen[key.Str] = true
		switch key.Str {
		case "id", "object", "status":
			valid = field.Type == gjson.String
		case "name", "residency", "external_key_id":
			valid = field.Type == gjson.Null || (field.Type == gjson.String && utf8.ValidString(field.Str))
		case "created_at", "archived_at":
			valid = field.Type == gjson.Null || field.Type == gjson.Number
		default:
			valid = false
		}
		if !valid || key.Str == "id" || key.Str == "object" {
			return valid
		}
		if !first {
			details.WriteByte(',')
		}
		first = false
		details.WriteString(key.Raw)
		details.WriteByte(':')
		details.WriteString(field.Raw)
		return true
	})
	if err := ctx.Err(); err != nil {
		return ProjectLifecycleReceipt{}, false, err
	}
	if !valid {
		return ProjectLifecycleReceipt{}, false, nil
	}
	details.WriteByte('}')
	receipt.ID, receipt.Details = id.Str, gjson.Parse(details.String())
	return receipt, true, nil
}
