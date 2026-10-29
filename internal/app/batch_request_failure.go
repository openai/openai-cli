package app

import (
	"net/http"
	"strings"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
)

// Keep the complete server payload when its error envelope is unfamiliar.
func showBatchRequestFailure(w *batchDetailWriter, value gjson.Result) bool {
	var payload gjson.Result
	switch value.Get("outcome").Str {
	case "failed":
		payload = value.Get("error")
	case "request failed":
		payload = value.Get("error_details")
	default:
		return false
	}
	failure := simpleBatchError(payload)
	if failure.Exists() {
		w.text("", "\n")
		w.text("record_message", readableValue(failure.Get("message")))
	} else if !payload.Exists() || payload.Type == gjson.Null {
		w.text("", "\nThe API returned no error message.\n")
	}
	code, kind := failure.Get("code"), failure.Get("type")
	status := int(value.Get("http_status").Int())
	if next := batchRequestRecovery(code.Str, kind.Str, status); next != "" {
		w.text("", "\n")
		w.text("heading", "Next  ")
		w.text("", next+"\n")
	}
	w.text("", "\n")
	if status != 0 {
		w.text("secondary", "HTTP        ")
		w.text("", strings.TrimSpace(value.Get("http_status").Raw+"  "+http.StatusText(status))+"\n")
	}
	for _, field := range []struct {
		label string
		value gjson.Result
	}{{"Code", code}, {"Type", kind}, {"Parameter", failure.Get("param")}, {"Request ID", value.Get("request_id")}} {
		if !field.value.Exists() || field.value.Type == gjson.Null || (field.label == "Type" && kind.Str == code.Str && code.Type == gjson.String) {
			continue
		}
		w.text("secondary", field.label+strings.Repeat(" ", 12-len(field.label)))
		w.text("", readableValue(field.value))
	}
	if !failure.Exists() && payload.Exists() && payload.Type != gjson.Null && w.err == nil {
		w.text("heading", "\nServer response\n")
		w.err = readable.Write(w, payload)
	}
	return true
}

// Only unwrap a single error field and known scalar members. Fallback preserves
// duplicate keys, additional fields and structured values in their original order.
func simpleBatchError(payload gjson.Result) gjson.Result {
	if nested := payload.Get("error"); nested.Exists() {
		count := 0
		payload.ForEach(func(_, _ gjson.Result) bool { count++; return count < 2 })
		if count != 1 {
			return gjson.Result{}
		}
		payload = nested
	}
	message := payload.Get("message")
	if !payload.IsObject() || message.Type != gjson.String || message.Str == "" {
		return gjson.Result{}
	}
	seen := map[string]bool{}
	familiar := true
	payload.ForEach(func(key, field gjson.Result) bool {
		switch key.Str {
		case "message", "code", "type", "param":
			familiar = !seen[key.Str] && (field.Type == gjson.String || field.Type == gjson.Null)
		default:
			familiar = false
		}
		seen[key.Str] = true
		return familiar
	})
	if !familiar {
		return gjson.Result{}
	}
	return payload
}

func batchRequestRecovery(code, kind string, status int) string {
	if status == 0 {
		switch code {
		case "batch_expired", "batch_cancelled":
			return "If you still need this result, submit this request in a new batch."
		case "server_error":
			return "Retry only this request in a new batch."
		}
	}
	if status == 429 || status == 0 {
		for _, category := range []string{code, kind} {
			switch category {
			case "insufficient_quota", "billing_hard_limit_reached", "credit_balance_exhausted", "organization_spend_limit_exceeded", "project_spend_limit_exceeded", "organization_usage_limit_exceeded":
				return "Check the project's billing and usage limits before retrying."
			}
		}
	}
	if (status == 400 || status == 404 || status == 422 || status == 0) && code == "model_not_found" {
		return "Check the model ID in this request and the project's access to it."
	}
	switch status {
	case 400, 422:
		return "Check the original input and correct it before submitting this request again."
	case 401, 403:
		return "Check the project's credentials and access to the requested resource."
	case 404:
		return "Check the resource ID and endpoint in the original input."
	case 429:
		return "Check the project's rate and usage limits before retrying."
	case 500, 502, 503, 504:
		return "Retry only this request in a new batch."
	}
	return ""
}
