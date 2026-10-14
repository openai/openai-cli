package custom

import (
	"reflect"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
)

func TestRequestStdinConsumers(t *testing.T) {
	text := "@-"
	for _, tc := range []struct {
		name string
		body any
		want int
	}{
		{"text", "@-", 1}, {"text pointer", &text, 1}, {"nil pointer", (*string)(nil), 0},
		{"binary", FilePathValue("-"), 1}, {"literal binary at", FilePathValue("@-"), 0},
		{"literal dash path", FilePathValue("./-"), 0}, {"literal escaped", `\@-`, 0},
		{"URI file", "@file://-", 1}, {"URI data", "@data://-", 1},
		{"binary URI literal", FilePathValue("@file://-"), 0},
		{"untrusted", untrustedStdinValue("@-"), 0},
		{"nested", map[string]any{"a": []any{"@-", map[string]any{"b": "@file://-"}}}, 2},
		{"array", [2]string{"@-", "@data://-"}, 2},
		{"nil", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := requestStdinConsumers(requestflag.RequestContents{Body: tc.body}, false)
			if got != tc.want || (err != nil) != (tc.want > 1) {
				t.Fatalf("count=%d err=%v want=%d", got, err, tc.want)
			}
		})
	}
	contents := requestflag.RequestContents{Body: "@-", Headers: map[string]any{"X-Test": "@-"}, Queries: map[string]any{"query": "@-"}}
	if count, err := requestStdinConsumers(contents, false); count != 3 || err == nil {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if count, err := requestStdinConsumers(requestflag.RequestContents{Body: "@-"}, true); count != 2 || err == nil {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if got := stdinConsumers(reflect.ValueOf(FilePathValue("/dev/stdin"))); got != 1 {
		t.Fatalf("stdin path count=%d", got)
	}
}
