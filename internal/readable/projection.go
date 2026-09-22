package readable

import (
	"strconv"
	"strings"

	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/tidwall/gjson"
)

// View describes the readable presentation of one API value. The original JSON
// stays separate, so explicit data formats never lose fields to a summary.
type View struct {
	Text    TextValue
	Summary gjson.Result
	Omitted bool
}

// Project chooses text or a resource summary without modifying the API value.
// Unknown response shapes use the complete labeled representation.
func Project(value gjson.Result, route transformers.Route) View {
	switch route.OutputKind {
	case transformers.OutputResponse, transformers.OutputPageItem, transformers.OutputStreamEvent:
	default:
		return View{}
	}
	view := View{Text: projectText(value, route)}
	if !view.Text.IsText && !view.Text.Skip {
		view.Summary, _, view.Omitted = summarize(value, route)
	}
	return view
}

// TextValue describes a text-only API result. A zero value asks the caller
// to present the original value in full. Text is never escaped or truncated here.
type TextValue struct {
	Text     string
	IsText   bool
	Delta    bool
	Skip     bool
	Snapshot bool
	Final    bool
	// Key identifies a stream's text part. Snapshot text replaces, rather than
	// appends to, earlier deltas with the same key. Final identifies a snapshot
	// of the entire response; the caller owns deduplication and stream state.
	// A response:<output_index>:* key covers all text parts of one output item.
	Key   string
	Parts []TextPart // component snapshots for aggregated completion events
	// Details contains completion metadata to display after text; zero means none.
	Details gjson.Result
}

// TextPart identifies one text component inside an aggregated snapshot.
type TextPart struct {
	Key, Text string
}

// projectText selects known text responses and events without discarding tool,
// image, mixed-content, or unsuccessful results. Route identity is used only
// where an API response has no unambiguous object or event discriminator.
func projectText(value gjson.Result, route transformers.Route) TextValue {
	// The custom transport boundary preserves native text/subtitle responses as
	// JSON strings before preparing output. Only these exact audio response
	// routes interpret a scalar string as text, retaining all original content.
	if value.Type == gjson.String && route.OutputKind == transformers.OutputResponse &&
		(route.Operation == "(resource) audio.transcriptions > (method) create" ||
			route.Operation == "(resource) audio.translations > (method) create") {
		return readableText(value)
	}
	// List entries are independent records: their IDs and repeated text matter.
	if route.OutputKind == transformers.OutputPageItem || !value.IsObject() || readableIssue(value) {
		return TextValue{}
	}
	if kind := value.Get("type").String(); kind != "" {
		return readableEvent(value, kind)
	}
	resource := readableResource(route.Operation)
	switch object := value.Get("object").String(); {
	case object == "response", object == "" && resource == "responses":
		return readableResponse(value)
	case object == "chat.completion.chunk":
		return readableChoices(value, true, true)
	case object == "chat.completion", object == "" && resource == "chat.completions":
		return readableChoices(value, true, route.OutputKind == transformers.OutputStreamEvent)
	case object == "text_completion", object == "" && resource == "completions":
		return readableChoices(value, false, route.OutputKind == transformers.OutputStreamEvent)
	case object == "" && (resource == "audio.transcriptions" || resource == "audio.translations"):
		if readableStatus(value, false) && readableFields(value,
			"text", "task", "language", "languages", "duration", "usage", "status", "error") {
			return readableText(value.Get("text"))
		}
		return TextValue{}
	default:
		return TextValue{}
	}
}

func readableResource(operation string) string {
	const prefix = "(resource) "
	if strings.HasPrefix(operation, prefix) {
		resource, _, ok := strings.Cut(strings.TrimPrefix(operation, prefix), " > (method) ")
		if ok {
			return resource
		}
	}
	return ""
}

func readableText(value gjson.Result) TextValue {
	if value.Type != gjson.String {
		return TextValue{}
	}
	return TextValue{Text: value.Str, IsText: true}
}

func readableIssue(value gjson.Result) bool {
	for _, key := range []string{"error", "incomplete_details"} {
		if field := value.Get(key); field.Exists() && field.Type != gjson.Null {
			return true
		}
	}
	return false
}

func readableStatus(value gjson.Result, progress bool) bool {
	status := value.Get("status")
	if !status.Exists() || status.Type == gjson.Null {
		return true
	}
	return status.Type == gjson.String && (status.Str == "completed" || progress && status.Str == "in_progress")
}

func readableResponse(value gjson.Result) TextValue {
	if readableIssue(value) || !readableStatus(value, false) {
		return TextValue{}
	}
	output := value.Get("output")
	if !output.IsArray() || len(output.Array()) == 0 {
		return TextValue{}
	}
	var texts []string
	for _, item := range output.Array() {
		if item.Get("type").String() == "reasoning" && readableEmptyReasoning(item) {
			continue
		}
		text, ok := readableMessage(item, false)
		if !ok {
			return TextValue{}
		}
		texts = append(texts, text...)
	}
	if len(texts) == 0 {
		return TextValue{}
	}
	return TextValue{Text: strings.Join(texts, "\n\n"), IsText: true}
}

func readableEmptyReasoning(value gjson.Result) bool {
	return readableStatus(value, false) && !readableIssue(value) &&
		readableFields(value, "id", "type", "status", "summary", "content", "encrypted_content") &&
		!readablePresent(value.Get("summary")) && !readablePresent(value.Get("content")) &&
		!readablePresent(value.Get("encrypted_content"))
}

func readableMessage(value gjson.Result, progress bool) ([]string, bool) {
	if !value.IsObject() || value.Get("type").String() != "message" ||
		!readableStatus(value, progress) || readableIssue(value) ||
		!readableFields(value, "id", "type", "role", "status", "phase", "content") {
		return nil, false
	}
	if role := value.Get("role"); role.Exists() && role.String() != "assistant" {
		return nil, false
	}
	content := value.Get("content")
	if !content.IsArray() {
		return nil, false
	}
	texts := make([]string, 0, len(content.Array()))
	for _, part := range content.Array() {
		text := readablePart(part)
		if !text.IsText {
			return nil, false
		}
		texts = append(texts, text.Text)
	}
	return texts, true
}

func readablePart(value gjson.Result) TextValue {
	switch value.Get("type").String() {
	case "output_text":
		if !readableFields(value, "type", "text", "annotations") || readablePresent(value.Get("annotations")) {
			return TextValue{}
		}
		return readableText(value.Get("text"))
	case "refusal":
		if readableFields(value, "type", "refusal") {
			return readableText(value.Get("refusal"))
		}
	}
	return TextValue{}
}

func readableChoices(value gjson.Result, chat, stream bool) TextValue {
	if !readableStatus(value, false) || readableIssue(value) || stream && readablePresent(value.Get("usage")) {
		return TextValue{}
	}
	choices := value.Get("choices")
	// A single text stream cannot represent interleaved alternatives faithfully.
	if !choices.IsArray() || len(choices.Array()) != 1 {
		return TextValue{}
	}
	choice := choices.Array()[0]
	if readablePresent(choice.Get("logprobs")) {
		return TextValue{}
	}
	if finish := choice.Get("finish_reason"); finish.Exists() && finish.Type != gjson.Null && finish.String() != "stop" {
		return TextValue{}
	}
	var result TextValue
	if chat {
		key := "message"
		if stream {
			key = "delta"
		}
		message := choice.Get(key)
		if !message.IsObject() || !readableFields(message, "role", "content", "refusal") {
			return TextValue{}
		}
		if role := message.Get("role"); readablePresent(role) && role.String() != "assistant" {
			return TextValue{}
		}
		var texts []string
		seenText := false
		for _, field := range []string{"content", "refusal"} {
			content := message.Get(field)
			if content.Type == gjson.String {
				seenText = true
				if content.Str != "" {
					texts = append(texts, content.Str)
				}
			} else if content.Exists() && content.Type != gjson.Null {
				return TextValue{}
			}
		}
		result = TextValue{Text: strings.Join(texts, "\n\n"), IsText: seenText}
		if stream && result.Text == "" && !readablePresent(value.Get("usage")) && !readablePresent(choice.Get("logprobs")) {
			return TextValue{Skip: true}
		}
	} else {
		result = readableText(choice.Get("text"))
	}
	if stream && result.IsText {
		result.Delta = true
		result.Key = "choice:" + strconv.FormatInt(choice.Get("index").Int(), 10)
	}
	return result
}

func readableEvent(value gjson.Result, kind string) TextValue {
	if !readableStatus(value, false) {
		return TextValue{}
	}
	var result TextValue
	var partTexts []string
	contentIndex := strconv.FormatInt(value.Get("content_index").Int(), 10)
	switch kind {
	case "response.output_text.delta", "response.refusal.delta", "transcript.text.delta":
		if !readableEventFields(value, "delta", "obfuscation") {
			return TextValue{}
		}
		result = readableText(value.Get("delta"))
		result.Delta = result.IsText
	case "response.output_text.done":
		if !readableEventFields(value, "text", "annotations") || readablePresent(value.Get("annotations")) {
			return TextValue{}
		}
		result = readableText(value.Get("text"))
		result.Snapshot = result.IsText
	case "response.refusal.done":
		if !readableEventFields(value, "refusal") {
			return TextValue{}
		}
		result = readableText(value.Get("refusal"))
		result.Snapshot = result.IsText
	case "transcript.text.done":
		if !readableEventFields(value, "text", "languages", "usage") {
			return TextValue{}
		}
		result = readableText(value.Get("text"))
		result.Snapshot, result.Final = result.IsText, result.IsText
		if result.IsText {
			result.Details = readableUsage(value)
		}
	case "response.completed":
		if !readableEventFields(value, "response") {
			return TextValue{}
		}
		response := value.Get("response")
		result = readableResponse(response)
		result.Snapshot, result.Final = result.IsText, result.IsText
		if result.IsText {
			result.Details = readableUsage(response)
			for index, item := range response.Get("output").Array() {
				if texts, ok := readableMessage(item, false); ok {
					result.Parts = append(result.Parts, readableParts(strconv.Itoa(index), texts)...)
				}
			}
		}
		return result
	case "response.content_part.added", "response.content_part.done":
		if !readableEventFields(value, "part") {
			return TextValue{}
		}
		result = readablePart(value.Get("part"))
		result.Snapshot = result.IsText
		if kind == "response.content_part.added" && result.IsText && result.Text == "" {
			return TextValue{Skip: true}
		}
	case "response.output_item.added", "response.output_item.done":
		if !readableEventFields(value, "item") {
			return TextValue{}
		}
		texts, ok := readableMessage(value.Get("item"), kind == "response.output_item.added")
		if !ok {
			return TextValue{}
		}
		if len(texts) == 0 && kind == "response.output_item.added" {
			return TextValue{Skip: true}
		}
		if len(texts) == 0 {
			return TextValue{}
		}
		if len(texts) > 1 {
			contentIndex = "*"
			partTexts = texts
		}
		result = TextValue{Text: strings.Join(texts, "\n\n"), IsText: true, Snapshot: true}
	case "response.created", "response.in_progress":
		response := value.Get("response")
		if !readableEventFields(value, "response") || !response.IsObject() || readableIssue(response) || !readableStatus(response, true) ||
			readablePresent(response.Get("output")) || readablePresent(response.Get("usage")) {
			return TextValue{}
		}
		return TextValue{Skip: true}
	default:
		return TextValue{}
	}
	if result.IsText {
		if strings.HasPrefix(kind, "transcript.") {
			result.Key = "transcript"
		} else {
			index := value.Get("output_index")
			item := strconv.FormatInt(index.Int(), 10)
			if !index.Exists() && value.Get("item_id").Type == gjson.String {
				item = value.Get("item_id").Str
			}
			result.Key = "response:" + item + ":" + contentIndex
			if partTexts != nil {
				result.Parts = readableParts(item, partTexts)
			}
		}
	}
	return result
}

func readableParts(outputIndex string, texts []string) []TextPart {
	parts := make([]TextPart, len(texts))
	for index, text := range texts {
		parts[index] = TextPart{Key: "response:" + outputIndex + ":" + strconv.Itoa(index), Text: text}
	}
	return parts
}

func readableUsage(value gjson.Result) gjson.Result {
	usage := value.Get("usage")
	if !readablePresent(usage) {
		return gjson.Result{}
	}
	return gjson.Parse(`{"usage":` + usage.Raw + `}`)
}

func readableEventFields(value gjson.Result, content ...string) bool {
	return readableFields(value, append(content,
		"type", "sequence_number", "item_id", "output_index", "content_index", "segment_id")...)
}

// Unknown nonempty content fields may carry a new output modality. Retain them
// through generic rendering instead of assuming that a nearby text field is all.
func readableFields(value gjson.Result, allowed ...string) bool {
	ok := value.IsObject()
	value.ForEach(func(key, field gjson.Result) bool {
		for _, name := range allowed {
			if key.Str == name {
				return true
			}
		}
		ok = !readablePresent(field)
		return ok
	})
	return ok
}

func readablePresent(value gjson.Result) bool {
	if !value.Exists() || value.Type == gjson.Null || value.Type == gjson.String && value.Str == "" {
		return false
	}
	if value.IsArray() {
		return len(value.Array()) > 0
	}
	if value.IsObject() {
		return len(value.Map()) > 0
	}
	return true
}
