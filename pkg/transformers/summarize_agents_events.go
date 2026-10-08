package transformers

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
)

// summarizeAgentsEvent projects only pinned public resource fields. A new field
// leaves the complete record visible instead of silently changing its meaning.
func summarizeAgentsEvent(ctx context.Context, value gjson.Result, slot, fields, omitted string) (readable.StreamEvent, bool, bool, error) {
	record := value.Get(slot)
	if !record.IsObject() || record.Get("id").Type != gjson.String || record.Get("id").Str == "" {
		return readable.StreamEvent{}, false, false, nil
	}
	summary, hidden, err := summarizeResourceFields(ctx, record, strings.Fields(fields), strings.Fields(omitted))
	if err != nil || !summary.Exists() {
		return readable.StreamEvent{}, false, false, err
	}
	var repeated []string
	for _, field := range []string{"session_id", "turn_id"} {
		if nested, outer := summary.Get(field), value.Get(field); nested.Type == gjson.String && nested.Str == outer.Str && outer.Type == gjson.String {
			repeated = append(repeated, field)
		}
	}
	if slot == "turn" && summary.Get("id").Str == value.Get("turn_id").Str {
		repeated = append(repeated, "id")
	}
	if len(repeated) > 0 {
		summary = streamResidual(summary, nil, repeated...)
	}
	return readable.StreamEvent{Details: streamResidual(value, map[string]gjson.Result{slot: summary},
		"type", "event_id", "output_index")}, true, hidden, nil
}

func summarizeAgentsToolEvent(ctx context.Context, value gjson.Result) (readable.StreamEvent, bool, bool, error) {
	fields, omitted := "id type status turn_id", ""
	switch value.Get("item.type").String() {
	case "function_call":
		fields += " name call_id"
		omitted = "arguments"
	case "mcp_call":
		fields += " name server_label error"
		omitted = "arguments output"
	case "computer_use_call":
		return summarizeAgentsComputerUseEvent(ctx, value)
	case "command_execution":
		fields += " exit_code"
		omitted = "command cwd duration_ms output"
	case "web_search_call":
		omitted = "action"
	case "create_subagent_call":
		fields += " agent_id model"
		omitted = "content reasoning_effort"
	case "send_subagent_input_call":
		fields += " sender_agent_id recipient_agent_id"
		omitted = "content"
	case "resume_subagent_call", "interrupt_subagent_call", "close_subagent_call":
		fields += " sender_agent_id recipient_agent_id"
	case "wait_for_subagents_call":
		fields += " sender_agent_id recipient_agent_ids"
	case "reasoning":
		omitted = "summary"
	default:
		return readable.StreamEvent{}, false, false, nil
	}
	return summarizeAgentsEvent(ctx, value, "item", fields, omitted)
}

// Only the pinned screenshot URL slot contains encoded media. Preserve future
// fields and malformed media; the ordinary stream hint reports the omission.
func summarizeAgentsComputerUseEvent(ctx context.Context, value gjson.Result) (readable.StreamEvent, bool, bool, error) {
	item := value.Get("item")
	if item.Get("id").Type != gjson.String || item.Get("id").Str == "" {
		return readable.StreamEvent{}, false, false, nil
	}
	output := item.Get("output")
	image := output.Get("image_url")
	hidden := false
	if output.Get("type").Str == "computer_screenshot" && image.Type == gjson.String {
		if encoded, ok := strings.CutPrefix(image.Str, "data:image/jpeg;base64,"); ok {
			// Reuse cancellable validation without allocating decoded image bytes.
			valid, err := encodedSummary(ctx, gjson.Result{Type: gjson.String, Str: encoded}, false)
			if err != nil {
				return readable.StreamEvent{}, false, false, err
			}
			if valid != "" {
				label := fmt.Sprintf("(JPEG screenshot; %d base64 characters)", len(encoded))
				start := image.Index - value.Index
				value = gjson.Parse(value.Raw[:start] + strconv.Quote(label) + value.Raw[start+len(image.Raw):])
				hidden = true
			}
		}
	}
	event, projected, omitted, err := summarizeAgentsEvent(ctx, value, "item", "id type status turn_id title output", "")
	if err == nil && !projected {
		// Unknown item fields stay visible, with only the known image slot reduced.
		return readable.StreamEvent{Details: value}, true, hidden, nil
	}
	return event, projected, hidden || omitted, err
}
