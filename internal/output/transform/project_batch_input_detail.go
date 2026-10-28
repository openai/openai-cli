package transform

import "github.com/tidwall/gjson"

// Browser details preserve every input field. Standalone summaries remain unchanged.
// Unfamiliar envelopes and duplicate fields use complete streaming rendering.
func projectBatchInputDetail(value gjson.Result) gjson.Result {
	if !batchKnownFields(value, "custom_id method url body") || !value.Get("body").IsObject() {
		return gjson.Result{}
	}
	body := value.Get("body")
	seen, unique := make(map[string]bool), true
	body.ForEach(func(key, _ gjson.Result) bool {
		unique = !seen[key.Str]
		seen[key.Str] = true
		return unique
	})
	if !unique {
		return gjson.Result{}
	}
	var out, settings batchRecordProjection
	out.add("request", batchRequestID(value))
	content := map[string]bool{"input": true, "messages": true, "prompt": true, "instructions": true, "suffix": true}
	for _, field := range []string{"input", "messages", "prompt", "instructions", "suffix"} {
		out.add(field, body.Get(field))
	}
	settings.add("model", body.Get("model"))
	settings.add("endpoint", value.Get("url"))
	settings.add("method", value.Get("method"))
	var configuration batchRecordProjection
	body.ForEach(func(key, field gjson.Result) bool {
		if !content[key.Str] && key.Str != "model" {
			configuration.add(key.Str, field)
		}
		return true
	})
	if len(configuration.fields) > 0 {
		// Keep body settings separate from envelope fields with the same names.
		settings.add("parameters", configuration.value())
	}
	out.add("settings", settings.value())
	return out.value()
}
