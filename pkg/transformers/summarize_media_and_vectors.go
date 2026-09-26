package transformers

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// Match the generated operation and response boundary before interpreting a
// field as media. Metadata, tool schemas, and unknown fields may use any names.
func selectReadableTransformer(route Route) Transformer {
	var fields func(gjson.Result) []gjson.Result
	vectors := false
	switch route {
	case Route{"(resource) images > (method) generate", OutputResponse},
		Route{"(resource) images > (method) edit", OutputResponse},
		Route{"(resource) images > (method) create_variation", OutputResponse}:
		fields = func(value gjson.Result) []gjson.Result { return arrayFields(value, "data", "b64_json") }
	case Route{"(resource) images > (method) generate", OutputStreamEvent},
		Route{"(resource) images > (method) edit", OutputStreamEvent}:
		fields = imageEventFields
	case Route{"(resource) embeddings > (method) create", OutputResponse}:
		fields = func(value gjson.Result) []gjson.Result { return arrayFields(value, "data", "embedding") }
		vectors = true
	case Route{"(resource) audio.speech > (method) create", OutputStreamEvent}:
		fields = speechEventFields
	case Route{"(resource) chat.completions > (method) create", OutputResponse},
		Route{"(resource) chat.completions > (method) retrieve", OutputResponse},
		Route{"(resource) chat.completions > (method) update", OutputResponse},
		Route{"(resource) chat.completions > (method) list", OutputPageItem}:
		fields = func(value gjson.Result) []gjson.Result { return arrayFields(value, "choices", "message.audio.data") }
	case Route{"(resource) chat.completions > (method) create", OutputStreamEvent}:
		fields = func(value gjson.Result) []gjson.Result { return arrayFields(value, "choices", "delta.audio.data") }
	case Route{"(resource) chat.completions.messages > (method) list", OutputPageItem}:
		fields = func(value gjson.Result) []gjson.Result { return []gjson.Result{value.Get("audio.data")} }
	case Route{"(resource) responses > (method) create", OutputResponse},
		Route{"(resource) responses > (method) retrieve", OutputResponse},
		Route{"(resource) responses > (method) cancel", OutputResponse},
		Route{"(resource) responses > (method) compact", OutputResponse},
		Route{"(resource) beta.responses > (method) create", OutputResponse},
		Route{"(resource) beta.responses > (method) retrieve", OutputResponse},
		Route{"(resource) beta.responses > (method) cancel", OutputResponse},
		Route{"(resource) beta.responses > (method) compact", OutputResponse}:
		fields = responseFields
	case Route{"(resource) responses > (method) create", OutputStreamEvent},
		Route{"(resource) responses > (method) retrieve", OutputStreamEvent},
		Route{"(resource) beta.responses > (method) create", OutputStreamEvent},
		Route{"(resource) beta.responses > (method) retrieve", OutputStreamEvent}:
		fields = responseEventFields
	case Route{"(resource) responses.input_items > (method) list", OutputPageItem},
		Route{"(resource) beta.responses.input_items > (method) list", OutputPageItem},
		Route{"(resource) conversations.items > (method) retrieve", OutputResponse},
		Route{"(resource) conversations.items > (method) list", OutputPageItem}:
		fields = responseItemFields
	case Route{"(resource) conversations.items > (method) create", OutputResponse}:
		fields = func(value gjson.Result) []gjson.Result { return responseArrayFields(value, "data") }
	default:
		return Identity
	}
	return func(ctx context.Context, value gjson.Result) (gjson.Result, error) {
		if err := ctx.Err(); err != nil {
			return gjson.Result{}, err
		}
		return summarizeFields(ctx, value, fields(value), vectors)
	}
}

// Each selector visits fixed API slots in source order, without walking through
// arbitrary objects that may contain user values or future response fields.
func arrayFields(value gjson.Result, arrayPath, fieldPath string) []gjson.Result {
	var fields []gjson.Result
	array := value.Get(arrayPath)
	if array.IsArray() {
		array.ForEach(func(_, item gjson.Result) bool {
			fields = append(fields, item.Get(fieldPath))
			return true
		})
	}
	return fields
}

func imageEventFields(value gjson.Result) []gjson.Result {
	switch value.Get("type").String() {
	case "image_generation.partial_image", "image_generation.completed", "image_edit.partial_image", "image_edit.completed":
		return []gjson.Result{value.Get("b64_json")}
	}
	return nil
}

func responseFields(value gjson.Result) []gjson.Result {
	return responseArrayFields(value, "output")
}

func responseArrayFields(value gjson.Result, path string) []gjson.Result {
	var fields []gjson.Result
	output := value.Get(path)
	if output.IsArray() {
		output.ForEach(func(_, item gjson.Result) bool {
			fields = append(fields, responseItemFields(item)...)
			return true
		})
	}
	return fields
}

func responseItemFields(value gjson.Result) []gjson.Result {
	if value.Get("type").String() == "image_generation_call" {
		return []gjson.Result{value.Get("result")}
	}
	return nil
}

func responseEventFields(value gjson.Result) []gjson.Result {
	switch value.Get("type").String() {
	case "response.audio.delta":
		return []gjson.Result{value.Get("delta")}
	case "response.image_generation_call.partial_image":
		return []gjson.Result{value.Get("partial_image_b64")}
	case "response.output_item.added", "response.output_item.done":
		return responseItemFields(value.Get("item"))
	case "response.created", "response.queued", "response.in_progress", "response.completed", "response.incomplete", "response.failed":
		return responseFields(value.Get("response"))
	}
	return nil
}

func summarizeFields(ctx context.Context, value gjson.Result, fields []gjson.Result, vectors bool) (gjson.Result, error) {
	var out strings.Builder
	offset := 0
	for _, field := range fields {
		if err := ctx.Err(); err != nil {
			return gjson.Result{}, err
		}
		summary, err := encodedSummary(ctx, field, vectors)
		if err != nil {
			return gjson.Result{}, err
		}
		if summary == "" {
			continue
		}
		// Replace only the selected value's source span. One pass preserves field
		// order, unknown JSON, and large numbers without copying once per item.
		start := field.Index - value.Index
		out.WriteString(value.Raw[offset:start])
		out.WriteString(strconv.Quote(summary))
		offset = start + len(field.Raw)
	}
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	if offset == 0 {
		return value, nil
	}
	out.WriteString(value.Raw[offset:])
	return gjson.Parse(out.String()), nil
}

func encodedSummary(ctx context.Context, value gjson.Result, vector bool) (string, error) {
	if !vector {
		if value.Type == gjson.String && value.Str != "" {
			// Validate without allocating decoded media. Unexpected text remains visible.
			reader := &base64SummaryReader{ctx: ctx, value: strings.NewReader(value.Str)}
			_, err := io.Copy(io.Discard, base64.NewDecoder(base64.StdEncoding, reader))
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if err == nil {
				return fmt.Sprintf("(%d base64 characters; use --format json for full value)", len(value.Str)), nil
			}
		}
		return "", nil
	}
	if !value.IsArray() {
		return "", nil
	}
	count, numeric := 0, true
	var err error
	value.ForEach(func(_, item gjson.Result) bool {
		if err = ctx.Err(); err != nil {
			return false
		}
		numeric = item.Type == gjson.Number
		count++
		return numeric
	})
	if err != nil {
		return "", err
	}
	if numeric && count > 0 {
		return fmt.Sprintf("(%d numbers; use --format json for full vector)", count), nil
	}
	return "", nil
}

// Poll the source so even the decoder's internal newline filtering can stop.
type base64SummaryReader struct {
	ctx   context.Context
	value *strings.Reader
}

func (r *base64SummaryReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.value.Read(p[:min(len(p), 32*1024)])
}
