package commandsuggest

import (
	"slices"
	"strconv"
	"strings"
)

// The shell owns the private endpoint. Links contain identifiers, never paths,
// command text, arguments, or credentials. Unsupported sessions stay plain.
func clickPrefix(session, generation string) string {
	if len(session) != 10 || len(generation) != 32 {
		return ""
	}
	for _, r := range session {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return ""
		}
	}
	for _, r := range generation {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return ""
		}
	}
	return "openai-insert://" + session + "/" + generation + "/"
}

// RenderText renders literal spans declared by the presenter. It does not
// discover suggestions in prose. Replacements use one pass, so nested commands
// and text returned by render cannot create new spans.
func RenderText(text string, suggestions []string, render func(string) string) string {
	type span struct{ text, marker string }
	var spans []span
	seen := make(map[string]bool)
	marker := "\x00"
	for strings.Contains(text, marker) {
		marker += "\x00"
	}
	for _, suggestion := range suggestions {
		if suggestion != "" && !seen[suggestion] && strings.Contains(text, suggestion) {
			seen[suggestion] = true
			spans = append(spans, span{suggestion, marker + "command:" + strconv.Itoa(len(spans)) + ":end"})
		}
	}
	ordered := slices.Clone(spans)
	// Prefer a complete command when another declared command is its prefix.
	slices.SortStableFunc(spans, func(a, b span) int { return len(b.text) - len(a.text) })
	var pairs []string
	for _, span := range spans {
		pairs = append(pairs, span.text, span.marker)
	}
	text = strings.NewReplacer(pairs...).Replace(text)
	pairs = nil
	for _, span := range ordered {
		if strings.Contains(text, span.marker) {
			pairs = append(pairs, span.marker, render(span.text))
		}
	}
	return strings.NewReplacer(pairs...).Replace(text)
}
