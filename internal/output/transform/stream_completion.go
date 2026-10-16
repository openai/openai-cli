package transform

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
			"response.refusal.delta", "response.refusal.done", "response.function_call_arguments.delta", "response.function_call_arguments.done",
			"response.audio.delta", "response.audio.done", "response.audio.transcript.delta", "response.audio.transcript.done",
			"response.code_interpreter_call_code.delta", "response.code_interpreter_call_code.done",
			"response.code_interpreter_call.completed", "response.code_interpreter_call.in_progress", "response.code_interpreter_call.interpreting",
			"response.compaction.compacting",
			"response.file_search_call.completed", "response.file_search_call.in_progress", "response.file_search_call.searching",
			"response.shell_call_command.added", "response.shell_call_command.delta", "response.shell_call_command.done",
			"response.shell_call_output_content.delta", "response.shell_call_output_content.done",
			"response.reasoning_summary_part.added", "response.reasoning_summary_part.done",
			"response.reasoning_summary_text.delta", "response.reasoning_summary_text.done", "response.reasoning_text.delta", "response.reasoning_text.done",
			"response.web_search_call.completed", "response.web_search_call.in_progress", "response.web_search_call.searching",
			"response.image_generation_call.completed", "response.image_generation_call.generating",
			"response.image_generation_call.in_progress", "response.image_generation_call.partial_image",
			"response.mcp_call_arguments.delta", "response.mcp_call_arguments.done",
			"response.mcp_call.completed", "response.mcp_call.failed", "response.mcp_call.in_progress",
			"response.mcp_list_tools.completed", "response.mcp_list_tools.failed", "response.mcp_list_tools.in_progress",
			"response.output_text.annotation.added", "response.custom_tool_call_input.delta", "response.custom_tool_call_input.done":
			// Resumed streams can omit response.created. Known tool completion
			// events finish only that tool, not the enclosing response.
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
