// Package readable renders API values as plain, labeled text.
package readable

import (
	"encoding/base64"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/tidwall/gjson"
)

// Write renders value in its original field order, without a pager or a table.
// Ordinary strings are complete, including multiline text. Known encoded media
// and numeric embedding vectors get a summary with an explicit JSON escape hatch.
// Terminal controls are escaped even when w is a redirected output stream.
func Write(w io.Writer, value gjson.Result) error {
	p := printer{w: w}
	p.value(value, 0, "", "", "", "")
	return p.err
}

type printer struct {
	w   io.Writer
	err error
}

func (p *printer) write(s string) {
	if p.err != nil {
		return
	}
	var n int
	n, p.err = io.WriteString(p.w, s)
	if p.err == nil && n != len(s) {
		p.err = io.ErrShortWrite
	}
}

func (p *printer) text(depth int, prefix, text string) {
	indent := strings.Repeat("  ", depth)
	continuation := indent
	if prefix != "" {
		continuation += "  "
	}
	for {
		line, rest, more := strings.Cut(text, "\n")
		p.write(indent)
		p.write(prefix)
		p.write(Text(line))
		p.write("\n")
		if !more || p.err != nil {
			return
		}
		indent, prefix, text = continuation, "", rest
	}
}

func (p *printer) value(value gjson.Result, depth int, prefix, parent, key, objectType string) {
	if p.err != nil {
		return
	}
	if summary := encodedSummary(value, parent, key, objectType); summary != "" {
		p.text(depth, prefix, summary)
		return
	}
	if !value.IsObject() && !value.IsArray() {
		text := value.String()
		if value.Type == gjson.Null {
			text = "(null)"
		} else if value.Type == gjson.Number && value.Raw != "" {
			text = value.Raw
		} else if value.Type == gjson.String && text == "" {
			text = "(empty string)"
		}
		p.text(depth, prefix, text)
		return
	}
	empty := true
	value.ForEach(func(_, _ gjson.Result) bool { empty = false; return false })
	if empty {
		text := "(empty object)"
		if value.IsArray() {
			text = "(empty list)"
		}
		p.text(depth, prefix, text)
		return
	}
	if prefix != "" {
		p.text(depth, "", strings.TrimSuffix(prefix, " "))
		depth++
	}
	if value.IsObject() {
		childObjectType := value.Get("type").String()
		value.ForEach(func(childKey, child gjson.Result) bool {
			p.value(child, depth, label(childKey.Str)+": ", key, childKey.Str, childObjectType)
			return p.err == nil
		})
		return
	}
	index, previousRecord := 0, false
	value.ForEach(func(_, child gjson.Result) bool {
		record := child.IsObject() || child.IsArray()
		if index > 0 && (record || previousRecord) {
			p.write("\n")
		}
		index++
		p.value(child, depth, strconv.Itoa(index)+". ", "", "", "")
		previousRecord = record
		return p.err == nil
	})
}

// Only these API fields contain encoded media by contract. Names such as data,
// content, or text on their own are deliberately not evidence of binary content.
func encodedSummary(value gjson.Result, parent, key, objectType string) string {
	media := key == "b64_json" || parent == "audio" && key == "data" ||
		objectType == "speech.audio.delta" && key == "audio" ||
		objectType == "image_generation_call" && key == "result" ||
		objectType == "response.image_generation_call.partial_image" && key == "partial_image_b64" ||
		objectType == "response.audio.delta" && key == "delta"
	if value.Type == gjson.String && media {
		// Check the encoding without allocating decoded media. Unexpected text in
		// these fields should remain visible rather than be silently summarized.
		if value.Str != "" {
			_, err := io.Copy(io.Discard, base64.NewDecoder(base64.StdEncoding, strings.NewReader(value.Str)))
			if err == nil {
				return fmt.Sprintf("(%d base64 characters; use --format json for full value)", len(value.Str))
			}
		}
	}
	if key != "embedding" || !value.IsArray() {
		return ""
	}
	count, numeric := 0, true
	value.ForEach(func(_, item gjson.Result) bool {
		numeric = item.Type == gjson.Number
		count++
		return numeric
	})
	if numeric && count > 0 {
		return fmt.Sprintf("(%d numbers; use --format json for full vector)", count)
	}
	return ""
}

// Canonical snake_case labels have a unique readable form. Quote every other
// spelling literally, so e.g. file_id, "File ID", and "file.id" stay distinct.
func label(key string) string {
	words := strings.Split(key, "_")
	for _, word := range words {
		if word == "" || word[0] < 'a' || word[0] > 'z' {
			return strconv.Quote(key)
		}
		for _, r := range word {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
				return strconv.Quote(key)
			}
		}
	}
	for i, word := range words {
		switch word {
		case "id", "api", "url", "json":
			words[i] = strings.ToUpper(word)
		case "ids", "urls":
			words[i] = strings.ToUpper(strings.TrimSuffix(word, "s")) + "s"
		default:
			if i == 0 {
				words[i] = strings.ToUpper(word[:1]) + word[1:]
			}
		}
	}
	return strings.Join(words, " ")
}

// jsonview covers terminal C0/C1 controls. Directional and line-separator
// characters also need escaping because they can visually reorder labels.
var directionalControls = strings.NewReplacer(
	"\u061c", `\u061c`, "\u200e", `\u200e`, "\u200f", `\u200f`,
	"\u2028", `\u2028`, "\u2029", `\u2029`, "\u202a", `\u202a`,
	"\u202b", `\u202b`, "\u202c", `\u202c`, "\u202d", `\u202d`,
	"\u202e", `\u202e`, "\u2066", `\u2066`, "\u2067", `\u2067`,
	"\u2068", `\u2068`, "\u2069", `\u2069`,
)

func sanitize(text string) string {
	return directionalControls.Replace(jsonview.SanitizeTerminalString(text))
}

// Text escapes terminal controls and directional marks while preserving ordinary
// newlines and tabs. It adds no newline, so callers may use it for streamed text.
func Text(text string) string {
	if !strings.ContainsAny(text, "\n\t") {
		return sanitize(text)
	}
	var b strings.Builder
	for {
		i := strings.IndexAny(text, "\n\t")
		if i < 0 {
			b.WriteString(sanitize(text))
			return b.String()
		}
		b.WriteString(sanitize(text[:i]))
		b.WriteByte(text[i])
		text = text[i+1:]
	}
}
