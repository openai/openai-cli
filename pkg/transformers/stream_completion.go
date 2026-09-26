package transformers

import "github.com/tidwall/gjson"

// StreamCompletionState remembers only observed API progress, not event bodies.
// It detects clean EOF after an unfinished result even when transport framing
// reports no error. Empty streams and wholly unfamiliar events remain unchanged.
type StreamCompletionState struct {
	responseStarted    bool
	responseFinished   bool
	choices            map[string]bool
	transcriptStarted  bool
	transcriptFinished bool
	speechStarted      bool
	speechFinished     bool
}

func (s *StreamCompletionState) Observe(value gjson.Result, route Route) {
	if !value.IsObject() {
		return
	}
	switch textStreamAPI(route) {
	case "responses":
		switch value.Get("type").String() {
		case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
			s.responseFinished = true
		case "response.created", "response.queued", "response.in_progress", "response.output_item.added", "response.output_item.done",
			"response.content_part.added", "response.content_part.done", "response.output_text.delta", "response.output_text.done",
			"response.refusal.delta", "response.refusal.done", "response.function_call_arguments.delta", "response.function_call_arguments.done":
			s.responseStarted = true
		}
	case "chat", "completions":
		api := textStreamAPI(route)
		object := value.Get("object").String()
		if value.Get("type").Exists() || !value.Get("choices").IsArray() ||
			(api == "chat" && object != "" && object != "chat.completion.chunk" && object != "chat.completion") ||
			(api == "completions" && object != "" && object != "text_completion") {
			return
		}
		value.Get("choices").ForEach(func(_, choice gjson.Result) bool {
			index, ok := streamIndex(choice.Get("index"))
			if choice.IsObject() && ok {
				if s.choices == nil {
					s.choices = make(map[string]bool)
				}
				finish := choice.Get("finish_reason")
				s.choices[index] = finish.Type == gjson.String && finish.Str != ""
			}
			return true
		})
	}
	if audioStreamAPI(route) == "" || !gjson.Valid(value.Raw) {
		return
	}
	switch audioStreamAPI(route) {
	case "transcriptions":
		switch value.Get("type").String() {
		case "transcript.text.delta", "transcript.text.segment":
			s.transcriptStarted = true
		case "transcript.text.done":
			if value.Get("text").Type == gjson.String {
				s.transcriptFinished = true
			}
		}
	case "speech":
		switch value.Get("type").String() {
		case "speech.audio.delta":
			s.speechStarted = true
		case "speech.audio.done":
			s.speechFinished = true
		}
	}
}

func (s *StreamCompletionState) CompletionError(route Route) string {
	switch textStreamAPI(route) {
	case "responses":
		if s.responseStarted && !s.responseFinished {
			return "the stream ended before the response completed"
		}
	case "chat", "completions":
		for _, finished := range s.choices {
			if !finished {
				return "the stream ended before all completion choices finished"
			}
		}
	}
	switch audioStreamAPI(route) {
	case "transcriptions":
		if s.transcriptStarted && !s.transcriptFinished {
			return "the stream ended before the transcription completed"
		}
	case "speech":
		if s.speechStarted && !s.speechFinished {
			return "the stream ended before the speech audio completed"
		}
	}
	return ""
}
