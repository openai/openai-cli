package transformers

import "github.com/tidwall/gjson"

// StreamFailure identifies unsuccessful terminal events using API event names,
// never arbitrary user fields or server-supplied error messages. Inspect the
// original event before field extraction or presentation so every output format
// has the same exit-status contract.
func StreamFailure(value gjson.Result) string {
	if !value.IsObject() {
		return ""
	}
	kind := value.Get("type").String()
	if kind == "" {
		// Assistants use an event/data envelope. A typed event's future fields
		// must not override its discriminator, even if one is named "event".
		if event := value.Get("event"); event.Type == gjson.String {
			kind = event.Str
		}
	}
	switch kind {
	case "error":
		return "the API reported an error while streaming"
	case "response.failed", "thread.run.failed", "thread.run.step.failed":
		return "the streamed response failed"
	case "response.incomplete", "thread.run.incomplete", "thread.message.incomplete":
		return "the streamed response is incomplete"
	case "response.cancelled", "response.canceled", "thread.run.cancelled", "thread.run.step.cancelled":
		return "the streamed response was cancelled"
	case "thread.run.expired", "thread.run.step.expired":
		return "the streamed run expired"
	case "response.completed", "response.done":
		// A completion event can carry a failed response (notably Realtime).
		switch value.Get("response.status").String() {
		case "failed":
			return "the streamed response failed"
		case "incomplete":
			return "the streamed response is incomplete"
		case "cancelled", "canceled":
			return "the streamed response was cancelled"
		}
	}
	return ""
}
