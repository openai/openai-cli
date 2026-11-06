package debugmiddleware

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

const (
	jsonCaptureInvalid   = "[omitted: invalid or incomplete JSON]"
	jsonCaptureTruncated = "[omitted: incomplete JSON; capture truncated]"
	jsonCaptureLimited   = "[omitted: JSON diagnostic exceeds rendering limit]"
	jsonCaptureSuffix    = " [capture truncated]"
	jsonCaptureRedacted  = `"<REDACTED>"`
)

type jsonCaptureContext uint8

const (
	jsonCaptureNested jsonCaptureContext = iota
	jsonCaptureRoot
	jsonCaptureResponse
	jsonCaptureUsage
	jsonCaptureUsageDetails
)

// redactJSONCapture renders only a diagnostic copy. The caller bounds captured
// input; this function bounds output to input length plus 128 bytes. Token order
// preserves duplicate keys. Invalid input never falls back to raw body bytes.
func redactJSONCapture(data []byte, truncated bool) string {
	if !json.Valid(data) {
		if truncated {
			return jsonCaptureTruncated
		}
		return jsonCaptureInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	output := jsonCaptureOutput{limit: len(data) + 128}
	if !writeCaptureJSONValue(decoder, &output, jsonCaptureRoot, false, false, 0) {
		if truncated {
			return jsonCaptureLimited + jsonCaptureSuffix
		}
		return jsonCaptureLimited
	}
	if truncated && !output.add(jsonCaptureSuffix) {
		return jsonCaptureLimited + jsonCaptureSuffix
	}
	return output.String()
}

type jsonCaptureOutput struct {
	strings.Builder
	limit int
}

func (out *jsonCaptureOutput) add(value string) bool {
	if len(value) > out.limit-out.Len() {
		return false
	}
	out.WriteString(value)
	return true
}

func writeCaptureJSONValue(
	decoder *json.Decoder,
	out *jsonCaptureOutput,
	context jsonCaptureContext,
	allowNumber, allowBool bool,
	depth int,
) bool {
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	switch value := token.(type) {
	case nil:
		return out.add("null")
	case string:
		return out.add(jsonCaptureRedacted)
	case json.Number:
		if allowNumber {
			return out.add(value.String())
		}
		return out.add(jsonCaptureRedacted)
	case bool:
		if allowBool {
			return out.add(strconv.FormatBool(value))
		}
		return out.add(jsonCaptureRedacted)
	case json.Delim:
		// This is a diagnostic rendering limit, never an API payload limit.
		if depth >= 64 {
			return false
		}
		if value == '[' {
			if !out.add("[") {
				return false
			}
			first := true
			for decoder.More() {
				if !first && !out.add(",") {
					return false
				}
				first = false
				// Arrays do not inherit scalar or usage-counter privileges.
				if !writeCaptureJSONValue(decoder, out, jsonCaptureNested, false, false, depth+1) {
					return false
				}
			}
			end, err := decoder.Token()
			return err == nil && end == json.Delim(']') && out.add("]")
		}
		if value != '{' || !out.add("{") {
			return false
		}
		first := true
		for decoder.More() {
			token, err := decoder.Token()
			key, ok := token.(string)
			if err != nil || !ok {
				return false
			}
			if !first && !out.add(",") {
				return false
			}
			first = false
			known, sensitive := captureJSONKeyPolicy(key)
			label := key
			if !known {
				label = redactedPlaceholder
			}
			if !out.add(strconv.Quote(label)) || !out.add(":") {
				return false
			}
			if !known || sensitive {
				if !skipCaptureJSONValue(decoder) || !out.add(jsonCaptureRedacted) {
					return false
				}
				continue
			}
			if !writeCaptureJSONValue(decoder, out, captureJSONChildContext(context, key),
				captureJSONNumberAllowed(context, key), captureJSONBoolAllowed(context, key), depth+1) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim('}') && out.add("}")
	}
	return false
}

// Skipping uses an iterative depth counter, including for deeply nested secrets.
func skipCaptureJSONValue(decoder *json.Decoder) bool {
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return true
	}
	if delimiter != '{' && delimiter != '[' {
		return false
	}
	depth := 1
	for depth > 0 {
		token, err = decoder.Token()
		if err != nil {
			return false
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
	}
	return true
}

func captureJSONKeyPolicy(key string) (known, sensitive bool) {
	switch key {
	case "input", "inputs", "messages", "message", "content", "output", "outputs", "text", "delta",
		"instructions", "prompt", "prompts", "arguments", "parameters", "metadata", "tools", "tool_calls",
		"functions", "function", "files", "file", "filename", "file_data", "file_url", "url", "image_url",
		"audio", "image", "images", "b64_json", "embedding", "embeddings", "refusal", "annotations",
		"logprobs", "top_logprobs", "error", "errors", "description", "headers", "authorization",
		"api_key", "access_token", "secret", "client_secret", "webhook_secret":
		return true, true
	case "id", "object", "type", "status", "model", "role", "data", "response", "usage", "choices",
		"created", "created_at", "completed_at", "owned_by", "finish_reason", "incomplete_details",
		"reason", "reasoning", "effort", "summary", "format", "response_format", "stream_options",
		"stream", "store", "background", "parallel_tool_calls", "has_more", "echo", "include_usage",
		"max_tokens", "max_completion_tokens", "max_output_tokens", "max_tool_calls", "temperature",
		"top_p", "n", "seed", "presence_penalty", "frequency_penalty", "sequence_number", "index",
		"output_index", "content_index", "item_index", "input_tokens", "output_tokens", "total_tokens",
		"prompt_tokens", "completion_tokens", "input_tokens_details", "output_tokens_details",
		"prompt_tokens_details", "completion_tokens_details", "cached_tokens", "cache_write_tokens",
		"reasoning_tokens", "audio_tokens", "image_tokens", "text_tokens", "accepted_prediction_tokens",
		"rejected_prediction_tokens":
		return true, false
	default:
		return false, false
	}
}

func captureJSONChildContext(parent jsonCaptureContext, key string) jsonCaptureContext {
	switch {
	case parent == jsonCaptureRoot && key == "response":
		return jsonCaptureResponse
	case (parent == jsonCaptureRoot || parent == jsonCaptureResponse) && key == "usage":
		return jsonCaptureUsage
	case parent == jsonCaptureUsage:
		switch key {
		case "input_tokens_details", "output_tokens_details", "prompt_tokens_details", "completion_tokens_details":
			return jsonCaptureUsageDetails
		}
	}
	return jsonCaptureNested
}

func captureJSONNumberAllowed(parent jsonCaptureContext, key string) bool {
	switch parent {
	case jsonCaptureRoot:
		switch key {
		case "max_tokens", "max_completion_tokens", "max_output_tokens", "max_tool_calls", "temperature",
			"top_p", "n", "seed", "presence_penalty", "frequency_penalty", "sequence_number", "index",
			"output_index", "content_index", "item_index":
			return true
		}
	case jsonCaptureUsage:
		switch key {
		case "input_tokens", "output_tokens", "total_tokens", "prompt_tokens", "completion_tokens":
			return true
		}
	case jsonCaptureUsageDetails:
		switch key {
		case "cached_tokens", "cache_write_tokens", "reasoning_tokens", "audio_tokens", "image_tokens",
			"text_tokens", "accepted_prediction_tokens", "rejected_prediction_tokens":
			return true
		}
	}
	return false
}

func captureJSONBoolAllowed(parent jsonCaptureContext, key string) bool {
	if parent == jsonCaptureRoot {
		switch key {
		case "stream", "store", "background", "parallel_tool_calls", "has_more", "echo":
			return true
		}
	}
	return false
}
