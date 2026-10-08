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

// reasoningItem uses summary positions as the same component keys as the four
// summary events. Unknown parts and opaque fields remain structured data.
func (s *AgentsStreamProjector) reasoningItem(ctx context.Context, value, item gjson.Result, final bool) (readable.StreamEvent, bool) {
	if !agentsReasoningItemValid(ctx, value, item) {
		return readable.StreamEvent{}, false
	}
	var event readable.StreamEvent
	var residual strings.Builder
	itemID := item.Get("id")
	residual.WriteByte('[')
	index, hasResidual, valid := 0, false, true
	item.Get("summary").ForEach(func(_, part gjson.Result) bool {
		if ctx.Err() != nil {
			valid = false
			return false
		}
		remaining := part
		if part.Get("type").Str == "summary_text" {
			text, ok := s.textPart(value, part.Get("text"), itemID, strconv.Itoa(index), "Reasoning", true, final)
			if !ok {
				valid = false
				return false
			}
			if text.Key != "" {
				event.Parts = append(event.Parts, text)
			}
			remaining = streamResidual(part, nil, "type", "text")
		}
		if index > 0 {
			residual.WriteByte(',')
		}
		if remaining.Exists() {
			hasResidual = true
			residual.WriteString(remaining.Raw)
		} else {
			residual.WriteString("null")
		}
		index++
		return true
	})
	if !valid {
		return readable.StreamEvent{}, false
	}
	residual.WriteByte(']')
	remaining := gjson.Result{}
	if hasResidual {
		remaining = gjson.Parse(residual.String())
	}
	itemDetails := streamResidual(item, map[string]gjson.Result{"summary": remaining}, "id", "type", "turn_id")
	event.Details = streamResidual(value, map[string]gjson.Result{"item": itemDetails},
		"type", "event_id", "session_id", "turn_id", "output_index")
	return event, true
}

func agentsReasoningItemValid(ctx context.Context, value, item gjson.Result) bool {
	if !agentsUniqueFields(value, "type", "event_id", "session_id", "turn_id", "output_index", "item") ||
		!agentsUniqueFields(item, "id", "type", "turn_id", "summary", "status") || value.Get("event_id").Type != gjson.String {
		return false
	}
	for _, id := range []gjson.Result{value.Get("session_id"), value.Get("turn_id"), item.Get("id"), item.Get("turn_id")} {
		if id.Type != gjson.String || id.Str == "" {
			return false
		}
	}
	if item.Get("turn_id").Str != value.Get("turn_id").Str || !item.Get("status").Exists() {
		return false
	}
	output := value.Get("output_index")
	if output.Raw != "null" || value.Get("type").Str != "agent.session.turn.item.added" {
		if output.Type != gjson.Number {
			return false
		}
		if _, err := strconv.ParseUint(output.Raw, 10, 32); err != nil {
			return false
		}
	}
	summary := item.Get("summary")
	if !summary.IsArray() {
		return false
	}
	valid := true
	summary.ForEach(func(_, part gjson.Result) bool {
		if ctx.Err() != nil {
			valid = false
			return false
		}
		valid = agentsUniqueFields(part, "type") && part.Get("type").Type == gjson.String
		if part.Get("type").Str == "summary_text" {
			valid = valid && agentsUniqueFields(part, "text") && part.Get("text").Type == gjson.String
		}
		return valid
	})
	return valid && ctx.Err() == nil
}
