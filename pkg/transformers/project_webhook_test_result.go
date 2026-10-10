package transformers

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/tidwall/gjson"
)

// ProjectWebhookTestResult separates test completion from receiver acceptance.
// Unknown or malformed results retain their complete readable representation.
func ProjectWebhookTestResult(ctx context.Context, value gjson.Result) (gjson.Result, error) {
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	if !value.IsObject() || !gjson.Valid(value.Raw) {
		return value, ctx.Err()
	}
	fields := make(map[string]gjson.Result, 5)
	valid := true
	value.ForEach(func(key, field gjson.Result) bool {
		if ctx.Err() != nil {
			return false
		}
		switch key.Str {
		case "object", "success", "status_code", "webhook_endpoint_id", "event_type":
		default:
			valid = false
		}
		if _, duplicate := fields[key.Str]; duplicate {
			valid = false
		}
		fields[key.Str] = field
		return valid
	})
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	status, err := strconv.ParseInt(fields["status_code"].Raw, 10, 64)
	if !valid || len(fields) != 5 || fields["object"].Type != gjson.String ||
		fields["object"].Str != "webhook_endpoint.test" || fields["success"].Type != gjson.True ||
		fields["status_code"].Type != gjson.Number || err != nil || status < 100 || status > 599 ||
		fields["webhook_endpoint_id"].Type != gjson.String || fields["event_type"].Type != gjson.String {
		return value, nil
	}
	result := "failed"
	if status >= 200 && status < 300 {
		result = "accepted"
	}
	// Quote response context so embedded newlines cannot impersonate result lines.
	text := fmt.Sprintf("Test request completed.\nDelivery %s: endpoint returned HTTP %d.\nWebhook endpoint ID: %s\nEvent type: %s",
		result, status, strconv.Quote(fields["webhook_endpoint_id"].Str), strconv.Quote(fields["event_type"].Str))
	encoded, err := json.Marshal(text)
	if err != nil {
		return gjson.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	return gjson.ParseBytes(encoded), nil
}
