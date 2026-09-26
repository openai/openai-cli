package transformers

import (
	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
)

// ProjectAudioResponse separates a transcript from its metadata for readable
// presentation. Native text and subtitle bodies reach this boundary as strings.
// The original value remains available to every explicit data format.
func ProjectAudioResponse(value gjson.Result, route Route) (readable.StreamEvent, bool) {
	if route.OutputKind != OutputResponse ||
		(route.Operation != "(resource) audio.transcriptions > (method) create" &&
			route.Operation != "(resource) audio.translations > (method) create") || !gjson.Valid(value.Raw) {
		return readable.StreamEvent{}, false
	}
	text := value
	var details gjson.Result
	if value.IsObject() {
		if value.Get("type").Exists() || value.Get("object").Exists() ||
			value.Get("error").Exists() && value.Get("error").Type != gjson.Null {
			return readable.StreamEvent{}, false
		}
		if status := value.Get("status"); status.Exists() && status.Type != gjson.Null &&
			(status.Type != gjson.String || status.Str != "completed") {
			return readable.StreamEvent{}, false
		}
		text = value.Get("text")
		details = streamResidual(value, nil, "text")
	}
	if text.Type != gjson.String {
		return readable.StreamEvent{}, false
	}
	return readable.StreamEvent{
		Parts:   []readable.StreamPart{{Key: "transcript", Text: text.Str, Snapshot: true}},
		Details: details,
	}, true
}
