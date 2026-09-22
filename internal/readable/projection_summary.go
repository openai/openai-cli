package readable

import (
	"encoding/json"
	"strings"

	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/tidwall/gjson"
)

// Summaries retain useful fields of known resources. Unknown fields and shapes
// fall back to the full value. The original JSON remains available separately.
func summarize(value gjson.Result, route transformers.Route) (gjson.Result, bool, bool) {
	if route.OutputKind == transformers.OutputStreamEvent || !value.IsObject() ||
		value.Get("id").Type != gjson.String || value.Get("id").Str == "" {
		return gjson.Result{}, false, false
	}
	resource := readableResource(route.Operation)
	object := value.Get("object").String()
	if deleted := value.Get("deleted"); deleted.Type == gjson.True || deleted.Type == gjson.False {
		return summaryDeletion(value, resource, object, deleted.Bool())
	}
	var fields, omitted string
	switch {
	case object == "model" && resource == "models":
		fields = "id owned_by shutdown_date"
		omitted = "object created"
	case object == "file" && resource == "files":
		fields = "id filename purpose bytes status status_details expires_at"
		omitted = "object created_at"
	case object == "batch" && resource == "batches":
		fields = "id status request_counts input_file_id output_file_id error_file_id errors endpoint model expires_at usage"
		omitted = "object completion_window created_at cancelled_at cancelling_at completed_at expired_at failed_at finalizing_at in_progress_at metadata"
	case object == "vector_store" && resource == "vector_stores":
		fields = "id name status file_counts usage_bytes expires_at"
		omitted = "object created_at last_active_at metadata expires_after"
	case object == "vector_store.file" && (resource == "vector_stores.files" || resource == "vector_stores.file_batches"):
		fields = "id vector_store_id status last_error usage_bytes attributes"
		omitted = "object created_at chunking_strategy"
	case object == "vector_store.files_batch" && resource == "vector_stores.file_batches":
		fields = "id vector_store_id status file_counts"
		omitted = "object created_at"
	case object == "fine_tuning.job" && resource == "fine_tuning.jobs":
		fields = "id status model fine_tuned_model training_file validation_file result_files trained_tokens error estimated_finish"
		omitted = "object created_at finished_at hyperparameters organization_id seed integrations metadata method"
	case object == "assistant" && resource == "beta.assistants":
		fields = "id name model description"
		omitted = "object created_at instructions metadata tools response_format temperature tool_resources top_p"
	case object == "thread.run" && (resource == "beta.threads.runs" || resource == "beta.threads"):
		fields = "id status thread_id assistant_id model required_action last_error incomplete_details usage expires_at"
		omitted = "object cancelled_at completed_at created_at failed_at instructions max_completion_tokens max_prompt_tokens metadata parallel_tool_calls response_format started_at tool_choice tools truncation_strategy temperature top_p"
	default:
		return gjson.Result{}, false, false
	}
	return summaryFields(value, strings.Fields(fields), strings.Fields(omitted), "")
}

func summaryDeletion(value gjson.Result, resource, object string, deleted bool) (gjson.Result, bool, bool) {
	var noun string
	switch {
	case resource == "models" && object == "model":
		noun = "model"
	case resource == "files" && object == "file":
		noun = "file"
	case resource == "vector_stores" && object == "vector_store.deleted":
		noun = "vector store"
	case resource == "vector_stores.files" && object == "vector_store.file.deleted":
		noun = "vector store file"
	case resource == "beta.assistants" && object == "assistant.deleted":
		noun = "assistant"
	case resource == "beta.threads" && object == "thread.deleted":
		noun = "thread"
	case resource == "beta.threads.messages" && object == "thread.message.deleted":
		noun = "message"
	case resource == "conversations" && object == "conversation.deleted":
		noun = "conversation"
	default:
		return gjson.Result{}, false, false
	}
	result := "Deleted " + noun + "."
	if !deleted {
		result = "Deletion was not confirmed."
	}
	return summaryFields(value, []string{"id", "deleted"}, []string{"object"}, result)
}

// Only declared fields may be hidden. A new server field, including an empty
// one, or a duplicate key falls back to full rendering instead of guessing its
// importance. Kept values use their original JSON so numbers never round.
func summaryFields(value gjson.Result, fields, omitted []string, result string) (gjson.Result, bool, bool) {
	allowed := make(map[string]bool, len(fields)+len(omitted))
	for _, field := range fields {
		allowed[field] = true
	}
	for _, field := range omitted {
		allowed[field] = true
	}
	values := make(map[string]gjson.Result, len(allowed))
	valid := true
	value.ForEach(func(key, child gjson.Result) bool {
		_, duplicate := values[key.Str]
		if !allowed[key.Str] || duplicate {
			valid = false
			return false
		}
		values[key.Str] = child
		return true
	})
	if !valid {
		return gjson.Result{}, false, false
	}
	var out strings.Builder
	out.WriteByte('{')
	count := 0
	appendField := func(name, raw string) {
		if count > 0 {
			out.WriteByte(',')
		}
		key, _ := json.Marshal(name)
		out.Write(key)
		out.WriteByte(':')
		out.WriteString(raw)
		count++
	}
	if result != "" {
		encoded, _ := json.Marshal(result)
		appendField("result", string(encoded))
	}
	kept := 0
	for _, key := range fields {
		field, exists := values[key]
		if exists && readablePresent(field) {
			appendField(key, field.Raw)
			kept++
		}
	}
	out.WriteByte('}')
	return gjson.Parse(out.String()), true, kept < len(values)
}
