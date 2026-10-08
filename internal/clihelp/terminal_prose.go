package clihelp

import "strings"

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
		case '`':
			start, end, next := inlineCode(text, i)
			if next < 0 {
				// An unfinished span remains literal, including the remaining text.
				out.WriteString(text[i:])
				return out.String()
			}
			out.WriteString(text[start:end])
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
