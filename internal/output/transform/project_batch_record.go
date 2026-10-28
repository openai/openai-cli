package transform

import (
	"context"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// ProjectBatchRecord selects the human view of one bounded, validated record.
// Unknown fields and malformed recognized shapes request complete rendering.
// Machine output never calls this projection.
func ProjectBatchRecord(ctx context.Context, value gjson.Result, kind string) (gjson.Result, error) {
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	if !gjson.Valid(value.Raw) {
		return gjson.Result{}, ctx.Err()
	}
	if id := value.Get("custom_id"); kind != "group" && id.Exists() && (id.Type != gjson.String || id.Str == "") {
		return gjson.Result{}, ctx.Err()
	}
	var out batchRecordProjection
	switch kind {
	case "group":
		out.add("request", batchRequestID(value))
		out.text("input_match", strings.ReplaceAll(value.Get("match").String(), "_", " "))
		if value.Get("match").Str != "matched" {
			out.add("input_records", value.Get("input_count"))
			out.add("selected_file_results", value.Get("result_count"))
			if value.Get("match").Str == "missing_result" {
				out.text("note", "No result in the selected file. It may be pending or in the other result file.")
			} else if value.Get("match").Str == "ambiguous" {
				out.text("note", "Duplicate IDs prevent a unique match. Inputs and results remain separate.")
			} else if value.Get("match").Str == "missing_input" {
				out.text("note", "No input record matches this custom_id.")
			} else {
				out.text("note", "A missing custom_id prevents matching this record.")
			}
			out.add("matching_issues", value.Get("issues"))
		}
	case "input", "matched_input":
		if !batchKnownFields(value, "custom_id method url body") || !value.Get("body").IsObject() || value.Get("method").Str != "POST" {
			return gjson.Result{}, ctx.Err()
		}
		body := value.Get("body")
		// Preserve all request content; only reviewed request configuration is omitted.
		if !batchKnownFields(body, batchInputContent+" "+batchEchoedSettings) {
			return gjson.Result{}, ctx.Err()
		}
		if kind == "input" {
			out.add("request", batchRequestID(value))
			out.add("endpoint", value.Get("url"))
		}
		for _, field := range strings.Fields(batchInputContent) {
			out.add(field, body.Get(field))
		}
	case "result", "matched_result":
		if !batchKnownFields(value, "id custom_id response error") {
			return gjson.Result{}, ctx.Err()
		}
		if kind == "result" {
			out.add("request", batchRequestID(value))
		}
		response, recordError := value.Get("response"), value.Get("error")
		if recordError.Exists() && recordError.Type != gjson.Null {
			out.text("outcome", "failed")
			out.add("error", recordError)
			if response.Type != gjson.Null {
				out.add("response", response)
			}
			break
		}
		if !batchKnownFields(response, "status_code request_id body") || response.Get("status_code").Type != gjson.Number {
			return gjson.Result{}, ctx.Err()
		}
		status, codeErr := strconv.ParseInt(response.Get("status_code").Raw, 10, 32)
		if codeErr != nil || status < 100 || status > 599 {
			return gjson.Result{}, ctx.Err()
		}
		if status < 200 || status >= 300 {
			out.text("outcome", "request failed")
			out.add("http_status", response.Get("status_code"))
			out.add("request_id", response.Get("request_id"))
			out.add("error_details", response.Get("body"))
			break
		}
		body := response.Get("body")
		switch body.Get("object").Str {
		case "response":
			if !batchResponseBody(&out, body) {
				return gjson.Result{}, ctx.Err()
			}
		case "chat.completion":
			if !batchChatBody(&out, body) {
				return gjson.Result{}, ctx.Err()
			}
		default:
			return gjson.Result{}, ctx.Err()
		}
		if status != 200 {
			out.add("http_status", response.Get("status_code"))
		}
	default:
		return gjson.Result{}, ctx.Err()
	}
	return out.value(), ctx.Err()
}

const batchInputContent = "model instructions input messages prompt suffix previous_response_id conversation metadata tools tool_choice response_format text prediction modalities audio"

// These fields describe echoed request configuration, not returned content.
const batchEchoedSettings = "background conversation max_output_tokens max_tool_calls max_tokens max_completion_tokens metadata parallel_tool_calls previous_response_id prompt_cache_key prompt_cache_options prompt_cache_retention reasoning safety_identifier service_tier store stream temperature text tool_choice tools top_logprobs logprobs top_p truncation user frequency_penalty presence_penalty n seed stop response_format prediction modalities audio"

func batchResponseBody(out *batchRecordProjection, body gjson.Result) bool {
	if !batchKnownFields(body, "id object created_at completed_at status error incomplete_details output usage model instructions access_programs billing moderation tool_usage "+batchEchoedSettings) || !body.Get("output").IsArray() {
		return false
	}
	status := body.Get("status").Str
	switch status {
	case "completed", "failed", "incomplete", "in_progress", "queued", "cancelled":
	default:
		return false
	}
	out.text("outcome", status)
	if value := body.Get("error"); value.Exists() && value.Type != gjson.Null {
		out.add("error", value)
	}
	if value := body.Get("incomplete_details"); value.Exists() && value.Type != gjson.Null {
		out.add("incomplete_details", value)
	}
	var blocks []gjson.Result
	refused, incomplete := false, false
	body.Get("output").ForEach(func(_, item gjson.Result) bool {
		parts, refusal, ok := batchResponseMessage(item)
		if !ok {
			parts = []gjson.Result{item}
		}
		blocks = append(blocks, parts...)
		refused = refused || refusal
		if state := item.Get("status"); state.Exists() && state.Type != gjson.Null && state.Str != "completed" {
			incomplete = true
		}
		item.Get("content").ForEach(func(_, part gjson.Result) bool { refused = refused || part.Get("type").Str == "refusal"; return true })
		return true
	})
	if refused {
		out.text("content_outcome", "includes a refusal")
	}
	if incomplete {
		out.text("output_state", "Some output items are not completed; see their reported status below.")
	}
	if len(blocks) == 0 {
		out.text("content_outcome", "no output returned")
	}
	out.add("output", batchOutputBlocks(blocks))
	out.add("model", body.Get("model"))
	out.add("usage", batchUsage(body.Get("usage")))
	for _, name := range []string{"access_programs", "billing", "moderation"} {
		if field := body.Get(name); !batchNullOrEmpty(field) {
			out.add(name, field)
		}
	}
	if usage := batchToolUsage(body.Get("tool_usage")); !batchNullOrEmpty(usage) {
		out.add("tool_usage", usage)
	}
	return true
}

func batchResponseMessage(item gjson.Result) ([]gjson.Result, bool, bool) {
	if !batchKnownFields(item, "id type status phase role content") || item.Get("type").Str != "message" || item.Get("role").Exists() && item.Get("role").Str != "assistant" || !item.Get("content").IsArray() {
		return nil, false, false
	}
	if status := item.Get("status"); status.Exists() && status.Str != "completed" {
		return nil, false, false
	}
	if phase := item.Get("phase"); phase.Exists() && phase.Type != gjson.Null && phase.Str != "final_answer" {
		return nil, false, false
	}
	var parts []gjson.Result
	refused := false
	item.Get("content").ForEach(func(_, part gjson.Result) bool {
		if part.Get("type").Str == "output_text" && batchKnownFields(part, "type text annotations logprobs") && part.Get("text").Type == gjson.String && batchNullOrEmpty(part.Get("annotations")) && batchNullOrEmpty(part.Get("logprobs")) {
			parts = append(parts, part.Get("text"))
		} else if part.Get("type").Str == "refusal" && batchKnownFields(part, "type refusal") && part.Get("refusal").Type == gjson.String {
			var refusal batchRecordProjection
			refusal.add("refusal", part.Get("refusal"))
			parts = append(parts, refusal.value())
			refused = true
		} else {
			parts = append(parts, part)
		}
		return true
	})
	return parts, refused, true
}

func batchChatBody(out *batchRecordProjection, body gjson.Result) bool {
	if !batchKnownFields(body, "id object created model choices usage service_tier system_fingerprint") || !body.Get("choices").IsArray() {
		return false
	}
	var choices []gjson.Result
	valid := true
	body.Get("choices").ForEach(func(_, choice gjson.Result) bool {
		if !batchKnownFields(choice, "index message finish_reason logprobs") || !batchKnownFields(choice.Get("message"), "role content refusal tool_calls function_call audio annotations") || choice.Get("message.role").Str != "assistant" {
			valid = false
			return false
		}
		var result batchRecordProjection
		result.add("choice", choice.Get("index"))
		switch choice.Get("finish_reason").Str {
		case "stop":
			result.text("outcome", "completed")
		case "length":
			result.text("outcome", "incomplete (output limit)")
		case "content_filter":
			result.text("outcome", "filtered")
		case "tool_calls", "function_call":
			result.text("outcome", "tool call returned")
		default:
			result.text("outcome", "completion not established")
		}
		if reason := choice.Get("finish_reason"); reason.Str != "stop" {
			result.add("finish_reason", reason)
		}
		message := choice.Get("message")
		refusal := message.Get("refusal")
		if refusal.Exists() && refusal.Type != gjson.Null && refusal.Type != gjson.String {
			valid = false
			return false
		}
		if refusal.Type == gjson.String && refusal.Str != "" {
			result.text("content_outcome", "refused")
			result.add("refusal", refusal)
		}
		for _, name := range []string{"content", "tool_calls", "function_call", "audio", "annotations"} {
			value := message.Get(name)
			if !batchNullOrEmpty(value) {
				if name == "content" {
					name = "output"
				}
				result.add(name, value)
			}
		}
		content := message.Get("content")
		if (batchNullOrEmpty(content) || content.Type == gjson.String && content.Str == "") && (batchNullOrEmpty(refusal) || refusal.Str == "") && batchNullOrEmpty(message.Get("tool_calls")) && batchNullOrEmpty(message.Get("function_call")) && batchNullOrEmpty(message.Get("audio")) {
			result.text("content_outcome", "no content returned")
		}
		if logs := choice.Get("logprobs"); !batchNullOrEmpty(logs) {
			result.add("logprobs", logs)
		}
		choices = append(choices, result.value())
		return true
	})
	if !valid {
		return false
	}
	if len(choices) == 0 {
		out.text("outcome", "completion not established (no choices returned)")
		out.add("choices", batchOutputBlocks(choices))
	} else if len(choices) == 1 {
		choices[0].ForEach(func(key, value gjson.Result) bool {
			if key.Str != "choice" || value.Raw != "0" {
				out.add(key.Str, value)
			}
			return true
		})
	} else {
		out.text("outcome", "see individual choices")
		out.add("choices", batchOutputBlocks(choices))
	}
	out.add("model", body.Get("model"))
	out.add("usage", batchUsage(body.Get("usage")))
	return true
}

// Only reviewed zero-valued usage counters may disappear from the human view.
// Totals, unfamiliar counters, and nonzero details remain visible.
func batchUsage(value gjson.Result) gjson.Result {
	if !value.IsObject() {
		return value
	}
	var out batchRecordProjection
	value.ForEach(func(key, field gjson.Result) bool {
		known := ""
		switch key.Str {
		case "input_tokens_details", "prompt_tokens_details":
			known = "cached_tokens cache_write_tokens audio_tokens"
		case "output_tokens_details", "completion_tokens_details":
			known = "reasoning_tokens audio_tokens accepted_prediction_tokens rejected_prediction_tokens"
		}
		if known != "" && batchKnownFields(field, known) {
			var details batchRecordProjection
			field.ForEach(func(name, counter gjson.Result) bool {
				if counter.Type != gjson.Number || counter.Raw != "0" {
					details.add(name.Str, counter)
				}
				return true
			})
			if len(details.fields) > 0 {
				out.add(key.Str, details.value())
			}
		} else {
			out.add(key.Str, field)
		}
		return true
	})
	return out.value()
}

func batchToolUsage(value gjson.Result) gjson.Result {
	if !value.IsObject() {
		return value
	}
	var out batchRecordProjection
	value.ForEach(func(key, field gjson.Result) bool {
		unused := false
		switch key.Str {
		case "web_search":
			unused = batchZeroCounters(field, "num_requests")
		case "image_gen":
			unused = batchKnownFields(field, "input_tokens output_tokens total_tokens input_tokens_details output_tokens_details")
			field.ForEach(func(name, counter gjson.Result) bool {
				if name.Str == "input_tokens_details" || name.Str == "output_tokens_details" {
					unused = unused && batchZeroCounters(counter, "image_tokens text_tokens")
				} else {
					unused = unused && counter.Type == gjson.Number && counter.Raw == "0"
				}
				return true
			})
		}
		if !unused {
			out.add(key.Str, field)
		}
		return true
	})
	return out.value()
}

func batchZeroCounters(value gjson.Result, keys string) bool {
	if !batchKnownFields(value, keys) {
		return false
	}
	zero := true
	value.ForEach(func(_, field gjson.Result) bool {
		zero = zero && field.Type == gjson.Number && field.Raw == "0"
		return true
	})
	return zero
}

func batchKnownFields(value gjson.Result, fields string) bool {
	if !value.IsObject() {
		return false
	}
	allowed := make(map[string]bool)
	for _, field := range strings.Fields(fields) {
		allowed[field] = true
	}
	seen, valid := make(map[string]bool), true
	value.ForEach(func(key, _ gjson.Result) bool {
		if !allowed[key.Str] || seen[key.Str] {
			valid = false
			return false
		}
		seen[key.Str] = true
		return true
	})
	return valid
}

func batchNullOrEmpty(value gjson.Result) bool {
	return !value.Exists() || value.Type == gjson.Null || value.Raw == "[]" || value.Raw == "{}"
}

func batchRequestID(value gjson.Result) gjson.Result {
	if id := value.Get("custom_id"); id.Type == gjson.String && id.Str != "" {
		return id
	}
	return gjson.Parse(`"(custom_id not reported)"`)
}

type batchRecordProjection struct{ fields []string }

func (p *batchRecordProjection) add(name string, value gjson.Result) {
	if value.Exists() {
		p.fields = append(p.fields, strconv.Quote(name)+":"+value.Raw)
	}
}
func (p *batchRecordProjection) text(name, value string) {
	p.fields = append(p.fields, strconv.Quote(name)+":"+strconv.Quote(value))
}
func (p *batchRecordProjection) value() gjson.Result {
	return gjson.Parse("{" + strings.Join(p.fields, ",") + "}")
}
func batchOutputBlocks(values []gjson.Result) gjson.Result {
	if len(values) == 1 {
		return values[0]
	}
	var out strings.Builder
	out.WriteByte('[')
	for i, value := range values {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString(value.Raw)
	}
	out.WriteByte(']')
	return gjson.Parse(out.String())
}
