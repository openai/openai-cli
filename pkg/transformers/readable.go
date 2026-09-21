package transformers

import (
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// ReadableValue describes a text-only API result. A zero value asks the caller
// to present the original value in full. Text is never escaped or truncated here.
type ReadableValue struct {
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
	Parts []ReadablePart // component snapshots for aggregated completion events
	// Details contains completion metadata to display after text; zero means none.
	Details gjson.Result
}

// ReadablePart identifies one text component inside an aggregated snapshot.
type ReadablePart struct {
	Key, Text string
}

// Readable projects known text responses and events without discarding tool,
// image, mixed-content, or unsuccessful results. Route identity is used only
// where an API response has no unambiguous object or event discriminator.
func Readable(value gjson.Result, route Route) ReadableValue {
	// The custom transport boundary preserves native text/subtitle responses as
	// JSON strings before preparing output. Only these exact audio response
	// routes interpret a scalar string as text, retaining all original content.
	if value.Type == gjson.String && route.OutputKind == OutputResponse &&
		(route.Operation == "(resource) audio.transcriptions > (method) create" ||
			route.Operation == "(resource) audio.translations > (method) create") {
		return readableText(value)
	}
	// List entries are independent records: their IDs and repeated text matter.
	if route.OutputKind == OutputPageItem || !value.IsObject() || readableIssue(value) {
		return ReadableValue{}
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
		return readableChoices(value, true, route.OutputKind == OutputStreamEvent)
	case object == "text_completion", object == "" && resource == "completions":
		return readableChoices(value, false, route.OutputKind == OutputStreamEvent)
	case object == "" && (resource == "audio.transcriptions" || resource == "audio.translations"):
		if readableStatus(value, false) && readableFields(value,
			"text", "task", "language", "languages", "duration", "usage", "status", "error") {
			return readableText(value.Get("text"))
		}
		return ReadableValue{}
	default:
		return ReadableValue{}
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

func readableText(value gjson.Result) ReadableValue {
	if value.Type != gjson.String {
		return ReadableValue{}
	}
	return ReadableValue{Text: value.Str, IsText: true}
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

func readableResponse(value gjson.Result) ReadableValue {
	if readableIssue(value) || !readableStatus(value, false) {
		return ReadableValue{}
	}
	output := value.Get("output")
	if !output.IsArray() || len(output.Array()) == 0 {
		return ReadableValue{}
	}
	var texts []string
	for _, item := range output.Array() {
		if item.Get("type").String() == "reasoning" && readableEmptyReasoning(item) {
			continue
		}
		text, ok := readableMessage(item, false)
		if !ok {
			return ReadableValue{}
		}
		texts = append(texts, text...)
	}
	if len(texts) == 0 {
		return ReadableValue{}
	}
	return ReadableValue{Text: strings.Join(texts, "\n\n"), IsText: true}
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

func readablePart(value gjson.Result) ReadableValue {
	switch value.Get("type").String() {
	case "output_text":
		if !readableFields(value, "type", "text", "annotations") || readablePresent(value.Get("annotations")) {
			return ReadableValue{}
		}
		return readableText(value.Get("text"))
	case "refusal":
		if readableFields(value, "type", "refusal") {
			return readableText(value.Get("refusal"))
		}
	}
	return ReadableValue{}
}

func readableChoices(value gjson.Result, chat, stream bool) ReadableValue {
	if !readableStatus(value, false) || readableIssue(value) || stream && readablePresent(value.Get("usage")) {
		return ReadableValue{}
	}
	choices := value.Get("choices")
	// A single text stream cannot represent interleaved alternatives faithfully.
	if !choices.IsArray() || len(choices.Array()) != 1 {
		return ReadableValue{}
	}
	choice := choices.Array()[0]
	if readablePresent(choice.Get("logprobs")) {
		return ReadableValue{}
	}
	if finish := choice.Get("finish_reason"); finish.Exists() && finish.Type != gjson.Null && finish.String() != "stop" {
		return ReadableValue{}
	}
	var result ReadableValue
	if chat {
		key := "message"
		if stream {
			key = "delta"
		}
		message := choice.Get(key)
		if !message.IsObject() || !readableFields(message, "role", "content", "refusal") {
			return ReadableValue{}
		}
		if role := message.Get("role"); readablePresent(role) && role.String() != "assistant" {
			return ReadableValue{}
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
				return ReadableValue{}
			}
		}
		result = ReadableValue{Text: strings.Join(texts, "\n\n"), IsText: seenText}
		if stream && result.Text == "" && !readablePresent(value.Get("usage")) && !readablePresent(choice.Get("logprobs")) {
			return ReadableValue{Skip: true}
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

func readableEvent(value gjson.Result, kind string) ReadableValue {
	if !readableStatus(value, false) {
		return ReadableValue{}
	}
	var result ReadableValue
	var partTexts []string
	contentIndex := strconv.FormatInt(value.Get("content_index").Int(), 10)
	switch kind {
	case "response.output_text.delta", "response.refusal.delta", "transcript.text.delta":
		if !readableEventFields(value, "delta", "obfuscation") {
			return ReadableValue{}
		}
		result = readableText(value.Get("delta"))
		result.Delta = result.IsText
	case "response.output_text.done":
		if !readableEventFields(value, "text", "annotations") || readablePresent(value.Get("annotations")) {
			return ReadableValue{}
		}
		result = readableText(value.Get("text"))
		result.Snapshot = result.IsText
	case "response.refusal.done":
		if !readableEventFields(value, "refusal") {
			return ReadableValue{}
		}
		result = readableText(value.Get("refusal"))
		result.Snapshot = result.IsText
	case "transcript.text.done":
		if !readableEventFields(value, "text", "languages", "usage") {
			return ReadableValue{}
		}
		result = readableText(value.Get("text"))
		result.Snapshot, result.Final = result.IsText, result.IsText
		if result.IsText {
			result.Details = readableUsage(value)
		}
	case "response.completed":
		if !readableEventFields(value, "response") {
			return ReadableValue{}
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
			return ReadableValue{}
		}
		result = readablePart(value.Get("part"))
		result.Snapshot = result.IsText
		if kind == "response.content_part.added" && result.IsText && result.Text == "" {
			return ReadableValue{Skip: true}
		}
	case "response.output_item.added", "response.output_item.done":
		if !readableEventFields(value, "item") {
			return ReadableValue{}
		}
		texts, ok := readableMessage(value.Get("item"), kind == "response.output_item.added")
		if !ok {
			return ReadableValue{}
		}
		if len(texts) == 0 && kind == "response.output_item.added" {
			return ReadableValue{Skip: true}
		}
		if len(texts) == 0 {
			return ReadableValue{}
		}
		if len(texts) > 1 {
			contentIndex = "*"
			partTexts = texts
		}
		result = ReadableValue{Text: strings.Join(texts, "\n\n"), IsText: true, Snapshot: true}
	case "response.created", "response.in_progress":
		response := value.Get("response")
		if !readableEventFields(value, "response") || !response.IsObject() || readableIssue(response) || !readableStatus(response, true) ||
			readablePresent(response.Get("output")) || readablePresent(response.Get("usage")) {
			return ReadableValue{}
		}
		return ReadableValue{Skip: true}
	default:
		return ReadableValue{}
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

func readableParts(outputIndex string, texts []string) []ReadablePart {
	parts := make([]ReadablePart, len(texts))
	for index, text := range texts {
		parts[index] = ReadablePart{Key: "response:" + outputIndex + ":" + strconv.Itoa(index), Text: text}
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
