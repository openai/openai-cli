package transformers

import (
	"context"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// SummarizeResource projects known list/get records without modifying API data.
// A zero summary asks the presenter to show the full value. Omitted reports
// whether the projection hides fields; presentation owns any explanatory hint.
func SummarizeResource(ctx context.Context, value gjson.Result, route Route) (gjson.Result, bool, error) {
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, false, err
	}
	operation, ok := strings.CutPrefix(route.Operation, "(resource) ")
	resource, method, found := strings.Cut(operation, " > (method) ")
	if !ok || !found || !(route.OutputKind == OutputResponse && method == "retrieve" ||
		route.OutputKind == OutputPageItem && (method == "list" || resource == "vector_stores.file_batches" && method == "list_files")) ||
		!value.IsObject() || value.Get("id").Type != gjson.String || value.Get("id").Str == "" || value.Get("object").Type != gjson.String {
		return gjson.Result{}, false, nil
	}
	object := value.Get("object").Str
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
	case object == "vector_store.file" && (resource == "vector_stores.files" || resource == "vector_stores.file_batches" && method == "list_files"):
		fields = "id vector_store_id status last_error usage_bytes attributes"
		omitted = "object created_at chunking_strategy"
	case object == "vector_store.files_batch" && resource == "vector_stores.file_batches" && method == "retrieve":
		fields = "id vector_store_id status file_counts"
		omitted = "object created_at"
	case object == "fine_tuning.job" && resource == "fine_tuning.jobs":
		fields = "id status model fine_tuned_model training_file validation_file result_files trained_tokens error estimated_finish"
		omitted = "object created_at finished_at hyperparameters organization_id seed integrations metadata method"
	case object == "assistant" && resource == "beta.assistants":
		fields = "id name model description"
		omitted = "object created_at instructions metadata tools response_format temperature tool_resources top_p"
	case object == "thread.run" && resource == "beta.threads.runs":
		fields = "id status thread_id assistant_id model required_action last_error incomplete_details usage expires_at"
		omitted = "object cancelled_at completed_at created_at failed_at instructions max_completion_tokens max_prompt_tokens metadata parallel_tool_calls response_format started_at tool_choice tools truncation_strategy temperature top_p"
	default:
		return gjson.Result{}, false, nil
	}
	return summarizeResourceFields(ctx, value, strings.Fields(fields), strings.Fields(omitted))
}

// Only declared fields may be hidden. New fields (even null), duplicate keys,
// and unfamiliar record shapes fall back to full rendering. Retained JSON is
// copied verbatim so large numbers and nested actions/errors stay intact.
func summarizeResourceFields(ctx context.Context, value gjson.Result, fields, omitted []string) (gjson.Result, bool, error) {
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
		if ctx.Err() != nil {
			return false
		}
		_, duplicate := values[key.Str]
		if !allowed[key.Str] || duplicate {
			valid = false
			return false
		}
		values[key.Str] = child
		return true
	})
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, false, err
	}
	if !valid {
		return gjson.Result{}, false, nil
	}
	var out strings.Builder
	out.WriteByte('{')
	kept := 0
	for _, key := range fields {
		if err := ctx.Err(); err != nil {
			return gjson.Result{}, false, err
		}
		field, exists := values[key]
		if !exists || !resourceFieldPresent(field) {
			continue
		}
		if kept > 0 {
			out.WriteByte(',')
		}
		out.WriteString(strconv.Quote(key))
		out.WriteByte(':')
		out.WriteString(field.Raw)
		kept++
	}
	out.WriteByte('}')
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, false, err
	}
	return gjson.Parse(out.String()), kept < len(values), nil
}

func resourceFieldPresent(value gjson.Result) bool {
	if !value.Exists() || value.Type == gjson.Null || value.Type == gjson.String && value.Str == "" {
		return false
	}
	if value.IsArray() || value.IsObject() {
		present := false
		value.ForEach(func(_, _ gjson.Result) bool { present = true; return false })
		return present
	}
	return true
}
