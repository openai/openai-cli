package transformers

import "github.com/tidwall/gjson"

// StreamFailure classifies ordinary SSE results before projection or extraction.
// Messages are fixed API classifications; the caller separately preserves the
// original event, including error details, for every presentation format.
func StreamFailure(value gjson.Result, route Route) string {
	api := textStreamAPI(route)
	if api == "" || !value.IsObject() {
		return ""
	}
	kind := value.Get("type").String()
	if kind == "error" {
		return "the API reported an error while streaming"
	}
	if api != "responses" {
		return ""
	}
	status := ""
	switch kind {
	case "response.failed":
		status = "failed"
	case "response.incomplete":
		status = "incomplete"
	case "response.cancelled", "response.canceled":
		status = "cancelled"
	case "response.completed", "response.done":
		status = value.Get("response.status").String()
	}
	switch status {
	case "failed":
		return "the streamed response failed"
	case "incomplete":
		return "the streamed response is incomplete"
	case "cancelled", "canceled":
		return "the streamed response was cancelled"
	}
	return ""
}
