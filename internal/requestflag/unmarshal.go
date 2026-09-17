package requestflag

import (
	"bytes"
	"encoding/json"

	"github.com/goccy/go-yaml"
)

// UnmarshalYAMLOrJSON decodes YAML or JSON input into v.
//
// JSON is valid YAML, but go-yaml only decodes a number as a float when it has
// a decimal point. A JSON number with an exponent and no fraction would
// otherwise decode as a string (https://github.com/goccy/go-yaml/issues/916).
// Python, JavaScript, and Go all emit that form for small floats, such as
// 1e-05 or 1e-7.
func UnmarshalYAMLOrJSON(data []byte, v any) error {
	return yaml.Unmarshal(addJSONExponentFractions(data), v)
}

// addJSONExponentFractions rewrites numbers such as 1e-05 in valid JSON as
// 1.0e-05, which go-yaml decodes to the same float64. All other input,
// including YAML, is returned unchanged so the rest of decoding is unaffected.
func addJSONExponentFractions(data []byte) []byte {
	if !json.Valid(data) {
		return data
	}

	var out []byte
	copied := 0
	for i := 0; i < len(data); i++ {
		switch c := data[i]; {
		case c == '"':
			// Skip the string, including escaped quotes. json.Valid guarantees
			// that it is terminated.
			for i++; data[i] != '"'; i++ {
				if data[i] == '\\' {
					i++
				}
			}
		case c == '-' || c >= '0' && c <= '9':
			start := i
			for i+1 < len(data) && isJSONNumberByte(data[i+1]) {
				i++
			}
			number := data[start : i+1]
			exponent := bytes.IndexAny(number, "eE")
			if exponent < 0 || bytes.IndexByte(number, '.') >= 0 {
				continue
			}
			out = append(out, data[copied:start+exponent]...)
			out = append(out, ".0"...)
			copied = start + exponent
		}
	}
	if out == nil {
		return data
	}
	return append(out, data[copied:]...)
}

func isJSONNumberByte(c byte) bool {
	return c >= '0' && c <= '9' || c == '-' || c == '+' || c == '.' || c == 'e' || c == 'E'
}
