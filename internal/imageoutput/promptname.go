package imageoutput

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	promptNameMaxWords = 8
	promptNameMaxBytes = 80
)

// NameFromPrompt makes a short filename stem from the description. It retains
// Unicode letters, numbers and their combining marks, dropping only an initial
// English article. The result contains no extension or path components. Empty
// results let the caller retain its usual timestamp fallback.
func NameFromPrompt(prompt string) string {
	var name, word strings.Builder
	words := 0
	first := true
	truncated := false

	finishWord := func() bool {
		if word.Len() == 0 {
			return false
		}
		text := word.String()
		if first {
			first = false
			if text == "a" || text == "an" || text == "the" {
				word.Reset()
				return false
			}
		}
		if words > 0 {
			if truncated || name.Len()+1+len(text) > promptNameMaxBytes {
				return true
			}
			name.WriteByte('-')
		}
		name.WriteString(text)
		words++
		word.Reset()
		return truncated || words == promptNameMaxWords
	}

	for _, r := range prompt {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || (unicode.IsMark(r) && word.Len() > 0) {
			r = unicode.ToLower(r)
			if word.Len()+utf8.RuneLen(r) > promptNameMaxBytes {
				truncated = true
				finishWord()
				break
			}
			word.WriteRune(r)
		} else if finishWord() {
			break
		}
	}
	finishWord()
	stem := name.String()
	if stem == "" {
		return ""
	}
	if ValidateName(stem) != nil {
		// Letter/number words already exclude filename punctuation and controls.
		// A remaining rejection is normally a Windows device name (e.g. CON).
		stem = "image-" + stem
		if len(stem) > promptNameMaxBytes || ValidateName(stem) != nil {
			return ""
		}
	}
	return stem
}
