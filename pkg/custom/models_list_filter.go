package custom

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Models accept one operand assembled from bare and quoted fragments. Only
// quoting and whitespace follow gcloud; operators and regex remain models-only.
func parseModelsListFilter(expression string) (byte, string, error) {
	rest := strings.TrimLeftFunc(expression, modelsListFilterSpace)
	if !strings.HasPrefix(rest, "id") {
		return 0, "", &modelsListError{"--filter accepts id=VALUE for exact matching or id~REGEX for regular expression matching."}
	}
	rest = strings.TrimLeftFunc(rest[2:], modelsListFilterSpace)
	if len(rest) == 0 || rest[0] != '=' && rest[0] != '~' {
		return 0, "", &modelsListError{"--filter accepts id=VALUE for exact matching or id~REGEX for regular expression matching."}
	}
	operator := rest[0]
	rest = strings.TrimLeftFunc(rest[1:], modelsListFilterSpace)
	if rest == "" {
		return 0, "", &modelsListError{"--filter requires an operand after = or ~. Use quotes for an empty operand."}
	}
	var operand strings.Builder
	var quote byte
	for position := 0; position < len(rest); {
		character := rest[position]
		if character == '\\' && position+1 < len(rest) {
			next := rest[position+1]
			if next == '\\' || quote != 0 && next == quote || quote == 0 && (next == '\'' || next == '"') {
				operand.WriteByte(next)
				position += 2
			} else {
				// Keep unknown escapes, including their complete following rune.
				// Copy original bytes so invalid UTF-8 never becomes replacement text.
				_, size := utf8.DecodeRuneInString(rest[position+1:])
				operand.WriteString(rest[position : position+1+size])
				position += 1 + size
			}
			continue
		}
		if quote != 0 {
			if character == quote {
				quote = 0
			} else {
				operand.WriteByte(character)
			}
			position++
			continue
		}
		if character == '\'' || character == '"' {
			quote = character
			position++
			continue
		}
		value, size := utf8.DecodeRuneInString(rest[position:])
		if modelsListFilterSpace(value) {
			if strings.TrimLeftFunc(rest[position:], modelsListFilterSpace) != "" {
				return 0, "", &modelsListError{"--filter accepts one operand. Quote spaces and Boolean-looking text; additional expressions are not supported."}
			}
			break
		}
		operand.WriteString(rest[position : position+size])
		position += size
	}
	if quote != 0 {
		return 0, "", &modelsListError{"--filter has an unterminated quote. Close the single or double quote."}
	}
	return operator, operand.String(), nil
}

// Python isspace also recognizes the four ASCII information separators.
func modelsListFilterSpace(value rune) bool {
	return unicode.IsSpace(value) || value >= '\u001c' && value <= '\u001f'
}
