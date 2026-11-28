package output

import (
	"bytes"

	"github.com/tidwall/pretty"
)

const jsonArrayWidth = 80

// Keep objects expanded and fit arrays without objects on one line when space allows.
// Width zero disables the dependency's one-line layout for both kinds of container.
func indentJSON(source []byte) []byte {
	formatted := pretty.PrettyOptions(source, &pretty.Options{Indent: "  "})
	out := formatted[:0]
	lineStart := 0
	var array [jsonArrayWidth]byte

	for i := 0; i < len(formatted); {
		end := i + 1
		switch formatted[i] {
		case '"':
			end = len(formatted)
			if length := jsonStringLength(formatted[i:]); length > 0 {
				end = i + length
			}
		case '\n':
			lineStart = len(out)
		case '[':
			remaining := jsonArrayWidth - (len(out) - lineStart)
			// The previous formatter excluded the newline after an array separator.
			if remaining >= 3 && lineStart > 0 && out[lineStart-1] == ',' &&
				len(bytes.TrimSpace(out[lineStart:])) == 0 {
				remaining++
			}
			if remaining > 3 {
				if size, consumed := inlineJSONArray(array[:remaining], formatted[i:]); consumed > 0 && size <= consumed {
					out = append(out, array[:size]...)
					i += consumed
					continue
				}
			}
		}
		out = append(out, formatted[i:end]...)
		i = end
	}
	return out
}

func inlineJSONArray(destination, source []byte) (size, consumed int) {
	depth := 0
	for i := 0; i < len(source); {
		start := i
		i++
		switch source[start] {
		case ' ', '\t', '\r', '\n':
			continue
		case '{':
			return 0, 0
		case '[':
			depth++
		case ']':
			depth--
		case '"':
			length := jsonStringLength(source[start:min(len(source), start+len(destination)-size)])
			if length == 0 {
				return 0, 0
			}
			i = start + length
		}

		if i-start > len(destination)-size {
			return 0, 0
		}
		size += copy(destination[size:], source[start:i])
		if depth == 0 {
			return size, i
		}
		if source[start] == ',' {
			if size == len(destination) {
				return 0, 0
			}
			destination[size] = ' '
			size++
		}
	}
	return 0, 0
}

func jsonStringLength(source []byte) int {
	for i := 1; i < len(source); i++ {
		switch source[i] {
		case '"':
			return i + 1
		case '\\':
			i++
		}
	}
	return 0
}
