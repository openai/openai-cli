package clihelp

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// terminalProse normalizes authored help, never request values or runtime data.
// Keep code blocks and commands literal; preserve malformed or escaped markup.
func terminalProse(text string) string {
	lines := reflowDescription(text)
	var fence descriptionFence
	for i, line := range lines {
		if !fence.contains(line) && !isCodeLine(line) {
			lines[i] = terminalInlineProse(line)
		}
	}
	return strings.Join(lines, "\n")
}

func terminalInlineProse(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		switch text[i] {
		case '\\':
			end := min(i+2, len(text))
			out.WriteString(text[i:end])
			i = end
		case '\'', '"':
			next := inlineQuote(text, i)
			if next < 0 {
				out.WriteString(text[i:])
				return out.String()
			}
			out.WriteString(text[i:next])
			i = next
		case '`':
			start, end, next := inlineCode(text, i)
			if next < 0 {
				// An unfinished span remains literal, including the remaining text.
				out.WriteString(text[i:])
				return out.String()
			}
			out.WriteString(text[start:end])
			i = next
		case '*', '_':
			start, end, next := inlineEmphasis(text, i)
			if next < 0 {
				out.WriteString(text[i:])
				return out.String()
			}
			if end > start {
				out.WriteString(terminalInlineProse(text[start:end]))
			} else {
				out.WriteString(text[i:next])
			}
			i = next
		case '[':
			labelEnd := inlineClosing(text, i, '[', ']')
			if labelEnd < 0 {
				out.WriteString(text[i:])
				return out.String()
			}
			next := labelEnd + 1
			if next >= len(text) || text[next] != '(' || i > 0 && text[i-1] == '!' {
				out.WriteString(text[i:next])
				i = next
				continue
			}
			destinationEnd := inlineClosing(text, next, '(', ')')
			if destinationEnd < 0 {
				out.WriteString(text[i:])
				return out.String()
			}
			label, destination := text[i+1:labelEnd], text[next+1:destinationEnd]
			if strings.HasPrefix(destination, "<") && strings.HasSuffix(destination, ">") {
				destination = destination[1 : len(destination)-1]
			}
			out.WriteString(terminalInlineProse(label))
			out.WriteString(" (" + destination + ")")
			i = destinationEnd + 1
		default:
			out.WriteByte(text[i])
			i++
		}
	}
	return out.String()
}

// Only prose-boundary spans are emphasis. Ambiguous path and wildcard tokens
// stay literal, including single-word *wildcards* and names such as _field_.
func inlineEmphasis(text string, start int) (int, int, int) {
	marker, length := text[start], 1
	for start+length < len(text) && text[start+length] == marker {
		length++
	}
	begin := start + length
	if length > 3 || begin == len(text) || !emphasisBoundary(text, start, true) {
		return 0, 0, begin
	}
	first, _ := utf8.DecodeRuneInString(text[begin:])
	if !unicode.IsLetter(first) && !unicode.IsNumber(first) && first != '`' && first != '[' {
		return 0, 0, begin
	}
	if emphasisURLToken(text, start) {
		return 0, 0, begin
	}
	for cursor := begin; cursor < len(text); cursor++ {
		if text[cursor] == '\\' {
			cursor++
			continue
		}
		if text[cursor] == '`' {
			if _, _, next := inlineCode(text, cursor); next >= 0 {
				cursor = next - 1
				continue
			}
			return 0, 0, -1
		}
		if text[cursor] == '\'' || text[cursor] == '"' {
			if next := inlineQuote(text, cursor); next >= 0 {
				cursor = next - 1
				continue
			}
			return 0, 0, -1
		}
		if text[cursor] == '[' {
			end := inlineClosing(text, cursor, '[', ']')
			if end >= 0 && end+1 < len(text) && text[end+1] == '(' {
				if end = inlineClosing(text, end+1, '(', ')'); end >= 0 {
					cursor = end
					continue
				}
			}
		}
		if text[cursor] != marker {
			continue
		}
		end := cursor + 1
		for end < len(text) && text[end] == marker {
			end++
		}
		last, _ := utf8.DecodeLastRuneInString(text[begin:cursor])
		if end-cursor == length && !unicode.IsSpace(last) && emphasisBoundary(text, end, false) && !emphasisURLToken(text, cursor) {
			body := text[begin:cursor]
			if !strings.ContainsFunc(body, unicode.IsSpace) && (length == 1 || marker == '_' || strings.ContainsAny(body, ".\\/*?#=")) {
				return 0, 0, begin
			}
			return begin, cursor, end
		}
		cursor = end - 1
	}
	return 0, 0, -1
}

func emphasisURLToken(text string, position int) bool {
	start := strings.LastIndexFunc(text[:position], unicode.IsSpace) + 1
	end := strings.IndexFunc(text[position:], unicode.IsSpace)
	if end < 0 {
		end = len(text) - position
	}
	return strings.Contains(text[start:position+end], "://")
}

func emphasisBoundary(text string, position int, opening bool) bool {
	if opening {
		if position == 0 {
			return true
		}
		before, _ := utf8.DecodeLastRuneInString(text[:position])
		return unicode.IsSpace(before) || strings.ContainsRune("([{:", before)
	}
	if position == len(text) {
		return true
	}
	after, _ := utf8.DecodeRuneInString(text[position:])
	return unicode.IsSpace(after) || strings.ContainsRune(").,;:!?]}\"'", after)
}

// Quoted argument fragments are literal. Apostrophes after word characters are
// not opening quotes, so contractions and possessives remain ordinary prose.
func inlineQuote(text string, start int) int {
	if start > 0 {
		previous, _ := utf8.DecodeLastRuneInString(text[:start])
		if unicode.IsLetter(previous) || unicode.IsNumber(previous) || previous == '_' {
			return start + 1
		}
	}
	for cursor := start + 1; cursor < len(text); cursor++ {
		if text[cursor] == '\\' {
			cursor++
			continue
		}
		if text[cursor] == text[start] && emphasisBoundary(text, cursor+1, false) {
			return cursor + 1
		}
	}
	return -1
}

// Backtick runs must match exactly. Backslashes inside code remain literal.
func inlineCode(text string, start int) (int, int, int) {
	length := 1
	for start+length < len(text) && text[start+length] == '`' {
		length++
	}
	for cursor := start + length; cursor < len(text); {
		relative := strings.IndexByte(text[cursor:], '`')
		if relative < 0 {
			break
		}
		cursor += relative
		end := cursor + 1
		for end < len(text) && text[end] == '`' {
			end++
		}
		if end-cursor == length {
			return start + length, cursor, end
		}
		cursor = end
	}
	return 0, 0, -1
}

// Balanced delimiters retain nested URL parentheses and escaped field paths.
func inlineClosing(text string, start int, open, close byte) int {
	depth, angle := 1, false
	for i := start + 1; i < len(text); i++ {
		if text[i] == '\\' {
			i++
			continue
		}
		if open == '(' && text[i] == '<' {
			angle = true
		} else if angle && text[i] == '>' {
			angle = false
		} else if !angle {
			if open == '[' && text[i] == '`' {
				if _, _, next := inlineCode(text, i); next >= 0 {
					i = next - 1
					continue
				}
			}
			if text[i] == open {
				depth++
			} else if text[i] == close {
				depth--
				if depth == 0 {
					return i
				}
			}
		}
	}
	return -1
}

// Fence markers must match; nested backticks cannot close a tilde code block.
type descriptionFence struct {
	marker byte
	length int
}

func (f *descriptionFence) contains(line string) bool {
	inside := f.length != 0
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || len(trimmed) < 3 || trimmed[0] != '`' && trimmed[0] != '~' {
		return inside
	}
	length := 1
	for length < len(trimmed) && trimmed[length] == trimmed[0] {
		length++
	}
	if inside {
		if trimmed[0] == f.marker && length >= f.length && strings.TrimSpace(trimmed[length:]) == "" {
			f.length = 0
		}
	} else if length >= 3 && (trimmed[0] != '`' || !strings.Contains(trimmed[length:], "`")) {
		f.marker, f.length = trimmed[0], length
		return true
	}
	return inside
}
