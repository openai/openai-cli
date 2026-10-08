package transformers

import (
	"context"
	"strings"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
)

// summarizeAgentsEvent projects only pinned public resource fields. A new field
// leaves the complete record visible instead of silently changing its meaning.
func summarizeAgentsEvent(ctx context.Context, value gjson.Result, slot, fields, omitted string) (readable.StreamEvent, bool, bool, error) {
	if !agentsUniqueFields(value, slot, "type", "event_id", "output_index", "session_id", "turn_id") {
		return readable.StreamEvent{}, false, false, ctx.Err()
	}
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
	summary, hidden, preserve, err := summarizeAgentsStreamItemImages(ctx, value)
	if err != nil {
		return readable.StreamEvent{}, false, false, err
	}
	if hidden || preserve {
		// Repeated item containers need their complete envelope. Replacing an item
		// through streamResidual would copy its first value over later occurrences.
		if hidden && !preserve && summary.Get("item.type").Str == "computer_use_call" {
			event, projected, _, err := summarizeAgentsComputerUseEvent(ctx, summary)
			if projected || err != nil {
				return event, projected, hidden, err
			}
		}
		return readable.StreamEvent{Details: summary}, true, hidden, nil
	}
	fields, omitted := "id type status turn_id", ""
	switch value.Get("item.type").String() {
	case "message", "function_call_output", "reasoning":
		return readable.StreamEvent{}, false, false, nil
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
	default:
		return readable.StreamEvent{}, false, false, nil
	}
	return summarizeAgentsEvent(ctx, value, "item", fields, omitted)
}

func summarizeAgentsStreamItemImages(ctx context.Context, value gjson.Result) (gjson.Result, bool, bool, error) {
	kind, valid := agentsImageDiscriminator(ctx, value, "type")
	if !valid || kind != "agent.session.turn.item.added" && kind != "agent.session.turn.item.done" {
		return value, false, true, ctx.Err()
	}
	var images []agentsImageSlot
	count, preserve := 0, false
	value.ForEach(func(key, item gjson.Result) bool {
		if ctx.Err() != nil {
			return false
		}
		if key.Str == "item" {
			count++
			itemType, valid := agentsImageDiscriminator(ctx, item, "type")
			preserve = preserve || !valid
			if itemType == "message" {
				_, valid = agentsImageDiscriminator(ctx, item, "role")
				preserve = preserve || !valid
			}
			collectAgentsItemImages(ctx, item, &images)
		}
		return true
	})
	summary, hidden, err := summarizeAgentsEncodedImages(ctx, value, images)
	return summary, hidden, preserve || count != 1, err
}

// Image spans are already reduced before the existing screenshot presentation.
func summarizeAgentsComputerUseEvent(ctx context.Context, value gjson.Result) (readable.StreamEvent, bool, bool, error) {
	item := value.Get("item")
	if item.Get("id").Type != gjson.String || item.Get("id").Str == "" {
		return readable.StreamEvent{}, false, false, nil
	}
	event, projected, omitted, err := summarizeAgentsEvent(ctx, value, "item", "id type status turn_id title output", "")
	if err == nil && !projected {
		// Unknown item fields stay visible, with only the known image slot reduced.
		return readable.StreamEvent{Details: value}, true, false, nil
	}
	return event, projected, omitted, err
}
