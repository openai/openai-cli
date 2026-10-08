package transformers

import (
	"context"
	"strconv"
	"strings"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
)

// reasoning projects one summary component. Project commits its bounded state
// only after this projection and the final context check succeed.
func (s *AgentsStreamProjector) reasoning(ctx context.Context, value gjson.Result, kind string) (readable.StreamEvent, bool) {
	final := strings.HasSuffix(kind, ".done")
	partEvent := strings.Contains(kind, "_part.")
	field := "delta"
	if final {
		field = "text"
	}
	if partEvent {
		field = "part"
	}
	if !agentsUniqueFields(value, "type", "event_id", "session_id", "turn_id", "item_id", "output_index", "summary_index", "status", field) {
		return readable.StreamEvent{}, false
	}
	if value.Get("event_id").Type != gjson.String {
		return readable.StreamEvent{}, false
	}
	// Both indices are required uint32 values in these four event schemas.
	// The generic streamIndex helper permits absent indices for other APIs.
	for _, name := range []string{"output_index", "summary_index"} {
		index := value.Get(name)
		if index.Type != gjson.Number {
			return readable.StreamEvent{}, false
		}
		if _, err := strconv.ParseUint(index.Raw, 10, 32); err != nil {
			return readable.StreamEvent{}, false
		}
	}
	text := value.Get(field)
	if partEvent {
		if !agentsUniqueFields(text, "type", "text") || text.Get("type").Str != "summary_text" {
			return readable.StreamEvent{}, false
		}
		if final && !value.Get("status").Exists() {
			return readable.StreamEvent{}, false
		}
		text = text.Get("text")
	}
	if ctx.Err() != nil {
		return readable.StreamEvent{}, false
	}
	part, ok := s.textPart(value, text, value.Get("item_id"), value.Get("summary_index").Raw, "Reasoning", partEvent || final, final)
	if !ok {
		return readable.StreamEvent{}, false
	}
	if !partEvent && s.duplicate(value) {
		return readable.StreamEvent{}, true
	}
	var event readable.StreamEvent
	if part.Key != "" {
		event.Parts = []readable.StreamPart{part}
	}
	if partEvent {
		remaining := streamResidual(value.Get("part"), nil, "type", "text")
		event.Details = streamResidual(value, map[string]gjson.Result{"part": remaining},
			"type", "event_id", "session_id", "turn_id", "item_id", "output_index", "summary_index")
	} else {
		event.Details = streamResidual(value, nil,
			"type", "event_id", "session_id", "turn_id", "item_id", "output_index", "summary_index", field)
	}
	return event, true
}
