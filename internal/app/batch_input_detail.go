package app

import (
	"io"
	"strings"

	batchcommands "github.com/openai/openai-cli/internal/commands/batches"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
)

// Browser input details style generated headings and labels, never input content.
func showBatchInputDetail(out io.Writer, value gjson.Result, decorate func(batchcommands.RecordTextStyle)) error {
	w := &batchDetailWriter{out: out, decorate: decorate}
	w.text("identity", strings.TrimSuffix(readableValue(value.Get("request")), "\n"))
	w.text("", "\n")
	value.ForEach(func(key, field gjson.Result) bool {
		if key.Str == "request" {
			return true
		}
		titles := map[string]string{
			"input": "Input", "messages": "Messages", "prompt": "Prompt",
			"instructions": "Instructions", "suffix": "Suffix", "settings": "Settings",
		}
		w.text("", "\n")
		if title, known := titles[key.Str]; known {
			w.text("heading", title+"\n")
			if key.Str == "settings" && field.IsObject() {
				field.ForEach(func(name, setting gjson.Result) bool {
					start := w.offset
					if w.err == nil {
						w.err = readable.Write(w, gjson.Parse("{"+name.Raw+":"+setting.Raw+"}"))
					}
					label := map[string]string{"model": "Model:", "endpoint": "Endpoint:", "method": "Method:", "parameters": "Parameters:"}[name.Str]
					if label != "" && w.offset >= start+int64(len(label)) {
						decorate(batchcommands.RecordTextStyle{Start: start, End: start + int64(len(label)), Role: "secondary"})
					}
					return w.err == nil
				})
			} else if w.err == nil {
				w.err = readable.Write(w, field)
			}
		} else if w.err == nil {
			w.err = readable.Write(w, gjson.Parse("{"+key.Raw+":"+field.Raw+"}"))
		}
		return w.err == nil
	})
	return w.err
}
