// Package readable renders API values as plain, labeled text.
package readable

import (
	"io"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// Write renders value in its original field order, without a pager or a table.
// Strings and array values are complete, including multiline text.
// Terminal controls are escaped even when w is a redirected output stream.
func Write(w io.Writer, value gjson.Result) error {
	p := printer{w: w}
	p.value(value, 0, "")
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

func (p *printer) value(value gjson.Result, depth int, prefix string) {
	if p.err != nil {
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
		value.ForEach(func(childKey, child gjson.Result) bool {
			p.value(child, depth, label(childKey.Str)+": ")
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
		p.value(child, depth, strconv.Itoa(index)+". ")
		previousRecord = record
		return p.err == nil
	})
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
