package transformers

import (
	"strconv"
	"strings"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
)

// ProjectTextStream selects text components without modifying the API event.
// Unknown content stays in Details; a false result preserves the complete event.
// The caller applies this only to readable output, never explicit data formats.
func ProjectTextStream(value gjson.Result, route Route) (readable.StreamEvent, bool) {
	if !value.IsObject() {
		return readable.StreamEvent{}, false
	}
	switch textStreamAPI(route) {
	case "responses":
		return projectResponseStream(value)
	case "chat", "completions":
		return projectChoiceStream(value, textStreamAPI(route) == "chat")
	}
	return readable.StreamEvent{}, false
}

func textStreamAPI(route Route) string {
	if route.OutputKind != OutputStreamEvent {
		return ""
	}
	switch route.Operation {
	case "(resource) responses > (method) create", "(resource) responses > (method) retrieve",
		"(resource) beta.responses > (method) create", "(resource) beta.responses > (method) retrieve":
		return "responses"
	case "(resource) chat.completions > (method) create":
		return "chat"
	case "(resource) completions > (method) create":
		return "completions"
	}
	return ""
}

func projectResponseStream(value gjson.Result) (readable.StreamEvent, bool) {
	var event readable.StreamEvent
	kind := value.Get("type").String()
	fields := map[string]gjson.Result{}
	switch kind {
	case "response.output_text.delta", "response.output_text.done", "response.refusal.delta", "response.refusal.done":
		field := "delta"
		snapshot := strings.HasSuffix(kind, ".done")
		refusal := strings.HasPrefix(kind, "response.refusal.")
		if snapshot {
			field = "text"
			if refusal {
				field = "refusal"
			}
		}
		output, content, ok := responseStreamIndexes(value)
		text := value.Get(field)
		if !ok || text.Type != gjson.String {
			return event, false
		}
		event.Parts = []readable.StreamPart{responseStreamPart(output, content, text.Str, snapshot, refusal)}
		fields[field] = gjson.Result{}
		if !refusal {
			fields = streamOmitEmpty(value, fields, "logprobs", "annotations")
		}
	case "response.content_part.added", "response.content_part.done":
		output, content, ok := responseStreamIndexes(value)
		if !ok {
			return event, false
		}
		part, rest, ok := projectResponsePart(value.Get("part"), output, content)
		if !ok {
			return event, false
		}
		event.Parts = []readable.StreamPart{part}
		fields["part"] = rest
	case "response.output_item.added", "response.output_item.done":
		output, _, ok := responseStreamIndexes(value)
		if !ok || !value.Get("item").IsObject() {
			return event, false
		}
		event.Parts, fields["item"] = projectResponseItem(value.Get("item"), output)
	case "response.created", "response.queued", "response.in_progress", "response.completed", "response.done",
		"response.failed", "response.incomplete", "response.cancelled", "response.canceled":
		response := value.Get("response")
		if !response.IsObject() || response.Get("object").Exists() && response.Get("object").Str != "response" {
			return event, false
		}
		var output []gjson.Result
		if items := response.Get("output"); items.IsArray() {
			items.ForEach(func(_, item gjson.Result) bool {
				parts, rest := projectResponseItem(item, strconv.Itoa(len(output)))
				event.Parts = append(event.Parts, parts...)
				output = append(output, rest)
				return true
			})
		}
		responseFields := map[string]gjson.Result{}
		if response.Get("output").IsArray() {
			responseFields["output"] = streamResidualArray(output)
		}
		responseFields = streamOmitEmpty(response, responseFields, "error", "incomplete_details", "usage", "moderation")
		streamOmitNormalStatus(response, responseFields)
		fields["response"] = streamResidual(response, responseFields,
			"id", "object", "created_at", "completed_at", "model", "instructions", "metadata",
			"parallel_tool_calls", "temperature", "tool_choice", "tools", "top_p", "background",
			"conversation", "max_output_tokens", "max_tool_calls", "previous_response_id", "prompt",
			"prompt_cache_key", "prompt_cache_options", "prompt_cache_retention", "reasoning",
			"safety_identifier", "service_tier", "text", "top_logprobs", "truncation", "user")
	default:
		return event, false
	}
	event.Details = streamResidual(value, fields, "type", "sequence_number", "item_id", "output_index", "content_index", "obfuscation")
	if event.Details.Exists() {
		// A repeated snapshot can emit details without emitting its text again.
		// Keep the identity so those details cannot appear attached to another
		// interleaved output or content part.
		event.Details = streamResidual(value, fields, "type", "sequence_number", "obfuscation")
	}
	return event, true
}

func projectResponseItem(item gjson.Result, output string) ([]readable.StreamPart, gjson.Result) {
	if item.IsObject() && item.Get("type").String() == "reasoning" {
		fields := make(map[string]gjson.Result)
		for _, name := range []string{"summary", "content", "encrypted_content"} {
			if value := item.Get(name); value.Type == gjson.Null || value.Raw == "[]" || value.Str == "" && value.Type == gjson.String {
				fields[name] = gjson.Result{}
			}
		}
		streamOmitNormalStatus(item, fields)
		return nil, streamResidual(item, fields, "id", "type")
	}
	if !item.IsObject() || item.Get("type").String() != "message" ||
		(item.Get("role").Exists() && item.Get("role").String() != "assistant") || !item.Get("content").IsArray() {
		return nil, item
	}
	var parts []readable.StreamPart
	var residual []gjson.Result
	item.Get("content").ForEach(func(_, value gjson.Result) bool {
		part, rest, ok := projectResponsePart(value, output, strconv.Itoa(len(residual)))
		if ok {
			parts = append(parts, part)
		} else {
			rest = value
		}
		residual = append(residual, rest)
		return true
	})
	fields := map[string]gjson.Result{"content": streamResidualArray(residual)}
	streamOmitNormalStatus(item, fields)
	rest := streamResidual(item, fields, "id", "type", "role")
	return parts, rest
}

func projectResponsePart(value gjson.Result, output, content string) (readable.StreamPart, gjson.Result, bool) {
	kind := value.Get("type").String()
	field := "text"
	if kind == "refusal" {
		field = "refusal"
	} else if kind != "output_text" {
		return readable.StreamPart{}, gjson.Result{}, false
	}
	text := value.Get(field)
	if text.Type != gjson.String {
		return readable.StreamPart{}, gjson.Result{}, false
	}
	fields := map[string]gjson.Result{}
	if kind == "output_text" {
		fields = streamOmitEmpty(value, fields, "annotations", "logprobs")
	}
	return responseStreamPart(output, content, text.Str, true, kind == "refusal"),
		streamResidual(value, fields, "type", field), true
}

func responseStreamPart(output, content, text string, snapshot, refusal bool) readable.StreamPart {
	label := ""
	if output != "0" || content != "0" {
		label = "Output " + output + ", part " + content
	}
	if refusal {
		if label != "" {
			label += ": "
		}
		label += "Refusal"
	}
	return readable.StreamPart{Key: "response:" + output + ":" + content, Text: text, Snapshot: snapshot, Label: label}
}

func responseStreamIndexes(value gjson.Result) (string, string, bool) {
	// A missing index without an ID retains the historical single-part default.
	// An ID-only event cannot be reconciled with positional final snapshots.
	if !value.Get("output_index").Exists() && (value.Get("item_id").Exists() || value.Get("item.id").Exists()) {
		return "", "", false
	}
	output, ok := streamIndex(value.Get("output_index"))
	content, contentOK := streamIndex(value.Get("content_index"))
	return output, content, ok && contentOK
}

func streamIndex(value gjson.Result) (string, bool) {
	if !value.Exists() {
		return "0", true
	}
	if value.Type != gjson.Number || value.Raw == "" {
		return "", false
	}
	index, err := strconv.ParseUint(value.Raw, 10, 64)
	return strconv.FormatUint(index, 10), err == nil
}

func projectChoiceStream(value gjson.Result, chat bool) (readable.StreamEvent, bool) {
	var event readable.StreamEvent
	object := value.Get("object").String()
	if value.Get("type").Exists() || (chat && object != "" && object != "chat.completion.chunk" && object != "chat.completion") ||
		(!chat && object != "" && object != "text_completion") || !value.Get("choices").IsArray() {
		return event, false
	}
	var choices []gjson.Result
	seen := make(map[string]bool)
	valid := true
	value.Get("choices").ForEach(func(_, choice gjson.Result) bool {
		index, ok := streamIndex(choice.Get("index"))
		if !choice.IsObject() || !ok || seen[index] {
			valid = false
			return false
		}
		seen[index] = true
		fields := map[string]gjson.Result{}
		if chat {
			field := "delta"
			if object == "chat.completion" {
				field = "message"
			}
			message := choice.Get(field)
			if !message.IsObject() || message.Get("role").Exists() && message.Get("role").String() != "assistant" {
				choices = append(choices, choice)
				return true
			}
			messageFields := map[string]gjson.Result{}
			for _, textField := range []string{"content", "refusal"} {
				if text := message.Get(textField); text.Type == gjson.String {
					event.Parts = append(event.Parts, choiceStreamPart(index, textField, text.Str, field == "message"))
					messageFields[textField] = gjson.Result{}
				} else if text.Type == gjson.Null {
					messageFields[textField] = gjson.Result{}
				}
			}
			fields[field] = streamResidual(message, messageFields, "role")
		} else if text := choice.Get("text"); text.Type == gjson.String {
			event.Parts = append(event.Parts, choiceStreamPart(index, "content", text.Str, false))
			fields["text"] = gjson.Result{}
		}
		fields = streamOmitEmpty(choice, fields, "finish_reason", "logprobs")
		if choice.Get("finish_reason").Str == "stop" {
			fields["finish_reason"] = gjson.Result{}
		}
		rest := streamResidual(choice, fields, "index")
		if rest.Exists() {
			// Explicit indexes keep residual tool calls and finish reasons attached
			// to the correct alternative, even when chunks reorder choices.
			rest = gjson.Parse(`{"index":` + index + `,` + strings.TrimPrefix(rest.Raw, "{"))
		}
		choices = append(choices, rest)
		return true
	})
	if !valid {
		return readable.StreamEvent{}, false
	}
	fields := streamOmitEmpty(value, map[string]gjson.Result{"choices": streamResidualArray(choices)}, "usage", "moderation")
	event.Details = streamResidual(value, fields,
		"id", "object", "created", "model", "service_tier", "system_fingerprint", "obfuscation")
	return event, true
}

func choiceStreamPart(index, field, text string, snapshot bool) readable.StreamPart {
	label := ""
	if index != "0" {
		label = "Choice " + index
	}
	if field == "refusal" {
		if label != "" {
			label += ": "
		}
		label += "Refusal"
	}
	return readable.StreamPart{Key: "choice:" + index + ":" + field, Text: text, Snapshot: snapshot, Label: label}
}

func streamOmitNormalStatus(value gjson.Result, fields map[string]gjson.Result) {
	switch value.Get("status").Str {
	case "completed", "in_progress", "queued":
		fields["status"] = gjson.Result{}
	}
}

func streamOmitEmpty(value gjson.Result, fields map[string]gjson.Result, names ...string) map[string]gjson.Result {
	for _, name := range names {
		field := value.Get(name)
		if field.Type == gjson.Null || field.Raw == "[]" || field.Raw == "{}" {
			fields[name] = gjson.Result{}
		}
	}
	return fields
}

// Retain unknown fields verbatim, including nulls and numbers beyond float64.
// Each caller names the known fields it can omit at its particular API slot.
func streamResidual(value gjson.Result, replacements map[string]gjson.Result, omitted ...string) gjson.Result {
	var out strings.Builder
	value.ForEach(func(key, field gjson.Result) bool {
		if replacement, ok := replacements[key.Str]; ok {
			field = replacement
			if field.Raw == "" {
				return true
			}
		} else {
			for _, name := range omitted {
				if key.Str == name {
					return true
				}
			}
		}
		if out.Len() == 0 {
			out.WriteByte('{')
		} else {
			out.WriteByte(',')
		}
		out.WriteString(key.Raw)
		out.WriteByte(':')
		out.WriteString(field.Raw)
		return true
	})
	if out.Len() == 0 {
		return gjson.Result{}
	}
	out.WriteByte('}')
	return gjson.Parse(out.String())
}

func streamResidualArray(values []gjson.Result) gjson.Result {
	var out strings.Builder
	present := false
	out.WriteByte('[')
	for index, value := range values {
		if index > 0 {
			out.WriteByte(',')
		}
		if value.Raw == "" {
			out.WriteString("null")
		} else {
			present = true
			out.WriteString(value.Raw)
		}
	}
	if !present {
		return gjson.Result{}
	}
	out.WriteByte(']')
	return gjson.Parse(out.String())
}
