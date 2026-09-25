package transformers

import (
	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
)

// ProjectAudioStream selects ordinary transcription deltas and final snapshots.
// Diarized segments retain their complete structured representation: segment
// deltas cannot safely be reconciled with a final transcript by position alone.
func ProjectAudioStream(value gjson.Result, route Route) (readable.StreamEvent, bool) {
	if audioStreamAPI(route) != "transcriptions" || !value.IsObject() || !gjson.Valid(value.Raw) {
		return readable.StreamEvent{}, false
	}
	field, snapshot := "delta", false
	switch value.Get("type").String() {
	case "transcript.text.delta":
		if value.Get("segment_id").Exists() {
			return readable.StreamEvent{}, false
		}
	case "transcript.text.done":
		field, snapshot = "text", true
	default:
		return readable.StreamEvent{}, false
	}
	text := value.Get(field)
	if text.Type != gjson.String {
		return readable.StreamEvent{}, false
	}
	return readable.StreamEvent{
		Parts:   []readable.StreamPart{{Key: "transcript", Text: text.Str, Snapshot: snapshot}},
		Details: streamResidual(value, nil, "type", field),
	}, true
}

func audioStreamAPI(route Route) string {
	if route.OutputKind == OutputStreamEvent {
		switch route.Operation {
		case "(resource) audio.transcriptions > (method) create":
			return "transcriptions"
		case "(resource) audio.speech > (method) create":
			return "speech"
		}
	}
	return ""
}

// Speech audio is encoded only in this known event slot. In particular, future
// event fields and metadata named audio must remain unchanged.
func speechEventFields(value gjson.Result) []gjson.Result {
	if value.Get("type").String() == "speech.audio.delta" {
		return []gjson.Result{value.Get("audio")}
	}
	return nil
}
