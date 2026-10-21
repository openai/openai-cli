package models

import (
	"strings"
	"testing"
)

func TestModelsListNeedsViewport(t *testing.T) {
	for _, test := range []struct {
		name          string
		content       string
		width, height int
		want          bool
	}{
		{name: "empty", width: 80, height: 2},
		{name: "empty line", content: "\n", width: 80, height: 2},
		{name: "single line", content: "model-first", width: 80, height: 2},
		{name: "trailing newline", content: "model-first\n", width: 80, height: 2},
		{name: "prompt line reserved", content: "model-first\nmodel-second\n", width: 80, height: 2, want: true},
		{name: "exact fit", content: "first\nsecond\nthird\n", width: 80, height: 4},
		{name: "exact fit without trailing newline", content: "first\nsecond\nthird", width: 80, height: 4},
		{name: "one line overflow", content: "first\nsecond\nthird\nfourth\n", width: 80, height: 4, want: true},
		{name: "intentional final blank line", content: "first\n\n", width: 80, height: 2, want: true},
		{name: "wrapped ID exact fit", content: "ID: model-" + strings.Repeat("x", 50) + "\n", width: 20, height: 4},
		{name: "wrapped ID overflow", content: "ID: model-" + strings.Repeat("x", 51) + "\n", width: 20, height: 4, want: true},
		{name: "wide Unicode exact fit", content: strings.Repeat("界", 10) + "\n", width: 10, height: 3},
		{name: "wide Unicode overflow", content: strings.Repeat("界", 11) + "\n", width: 10, height: 3, want: true},
		{name: "combining marks exact fit", content: strings.Repeat("e\u0301", 20) + "\n", width: 10, height: 3},
		{name: "combining marks overflow", content: strings.Repeat("e\u0301", 21) + "\n", width: 10, height: 3, want: true},
		{name: "ANSI styling has no width", content: "\x1b[31m" + strings.Repeat("x", 20) + "\x1b[0m\n", width: 10, height: 3},
		{name: "minimum height reserves prompt", content: "model-first\n", width: 80, height: 1, want: true},
		{name: "minimum height overflow", content: "model-first\nmodel-second\n", width: 80, height: 1, want: true},
		{name: "empty result reserves prompt", content: "No results.\n", width: 80, height: 1, want: true},
		{name: "tabbed fallback overflows", content: "ID: model_a\nName: abcdef\t12345\n", width: 20, height: 3, want: true},
		{name: "tabbed fallback fits", content: "ID: model_a\nName: abcdef\t12345\n", width: 20, height: 4},
		{name: "tab cancels pending wrap", content: "ID: model_a\nName: abcdefghijklmn\tZ\n", width: 20, height: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := NeedsViewport(test.content, test.width, test.height); got != test.want {
				t.Fatalf("NeedsViewport(%q, %d, %d) = %v, want %v", test.content, test.width, test.height, got, test.want)
			}
		})
	}
}
