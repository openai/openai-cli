package transform

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
	return projectWebhookTestResult(ctx, value, true)
}

// ProjectWebhookTestOutcome leaves request context to the command presenter.
func ProjectWebhookTestOutcome(ctx context.Context, value gjson.Result) (gjson.Result, error) {
	return projectWebhookTestResult(ctx, value, false)
}

func projectWebhookTestResult(ctx context.Context, value gjson.Result, includeResponseContext bool) (gjson.Result, error) {
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
	if !valid || fields["success"].Type != gjson.True || fields["status_code"].Type != gjson.Number ||
		err != nil || status < 100 || status > 599 {
		return value, nil
	}
	// The captured minimal response has only success and status_code. Optional
	// metadata must still match the known shape when the server supplies it.
	if object, present := fields["object"]; present && (object.Type != gjson.String || object.Str != "webhook_endpoint.test") {
		return value, nil
	}
	for _, key := range []string{"webhook_endpoint_id", "event_type"} {
		if field, present := fields[key]; present && field.Type != gjson.String {
			return value, nil
		}
	}
	result := "failed"
	if status >= 200 && status < 300 {
		result = "accepted"
	}
	text := fmt.Sprintf("Test request completed.\nDelivery %s: endpoint returned HTTP %d.", result, status)
	if includeResponseContext {
		// Quote response context so embedded newlines cannot impersonate result lines.
		if field, present := fields["webhook_endpoint_id"]; present {
			text += "\nWebhook endpoint ID: " + strconv.Quote(field.Str)
		}
		if field, present := fields["event_type"]; present {
			text += "\nEvent type: " + strconv.Quote(field.Str)
		}
	}
	encoded, err := json.Marshal(text)
	if err != nil {
		return gjson.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	return gjson.ParseBytes(encoded), nil
}
