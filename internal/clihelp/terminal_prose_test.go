package clihelp

import (
	"strings"
	"testing"
)

func TestTerminalProseNormalizesCodeAndLinks(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"every code span", "Choose `transparent`, `opaque`, or `auto`.", "Choose transparent, opaque, or auto."},
		{"reference", "Read [the guide](https://example.invalid/guide#inputs).", "Read the guide (https://example.invalid/guide#inputs)."},
		{"source wrapping", "Read [the\nguide](https://example.invalid/guide).", "Read the guide (https://example.invalid/guide)."},
		{"balanced URL", "See [a guide](https://example.invalid/a_(b)?q=x%20y#c).", "See a guide (https://example.invalid/a_(b)?q=x%20y#c)."},
		{"angle URL", "See [guide](<https://example.invalid/a(b>).", "See guide (https://example.invalid/a(b)."},
		{"escaped URL", `See [guide](https://example.invalid/a\(b\)).`, `See guide (https://example.invalid/a\(b\)).`},
		{"link title", `See [guide](https://example.invalid "The guide").`, `See guide (https://example.invalid "The guide").`},
		{"code label", "Select [`data[0]`](https://example.invalid/fields).", "Select data[0] (https://example.invalid/fields)."},
		{"literal brackets", "Select data[0] and `data.#(id==\"x\")#`.", "Select data[0] and data.#(id==\"x\")#."},
		{"field escapes", "Select `fav\\.movie` or `path\\*`.", `Select fav\.movie or path\*.`},
		{"code spaces", "Keep `two  spaces` and ``literal `tick` text``.", "Keep two  spaces and literal `tick` text."},
		{"literal link in code", "Keep `[label](https://example.invalid)` literal.", "Keep [label](https://example.invalid) literal."},
		{"escaped markup", "Keep \\`literal\\` and \\[label](https://example.invalid).", "Keep \\`literal\\` and \\[label](https://example.invalid)."},
		{"mismatched ticks", "Keep ``unclosed `code` literal.", "Keep ``unclosed `code` literal."},
		{"unclosed code", "Keep `unclosed [guide](url).", "Keep `unclosed [guide](url)."},
		{"unclosed label", "Keep [unclosed `code` literal.", "Keep [unclosed `code` literal."},
		{"unclosed destination", "Keep [guide](https://example.invalid/(path).", "Keep [guide](https://example.invalid/(path)."},
		{"image syntax", "Keep ![literal](example.png).", "Keep ![literal](example.png)."},
		{"reference syntax", "Keep [label][reference].", "Keep [label][reference]."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := terminalProse(tc.source); got != tc.want {
				t.Fatalf("terminal prose = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestTerminalProsePreservesBlocksCommandsAndLists(t *testing.T) {
	source := "Use `auto`.\n\n- Keep `first` and `second`.\n- Read [the guide](https://example.invalid).\n\n" +
		"~~~sh\nopenai responses create --input '`raw` [literal](url)'\n```\n[still literal](url)\n~~~\n" +
		"Afterwards, use `text`.\n\n" +
		"````text\n```\n[literal](url)\n````\n" +
		"    literal `tick` [label](url)\n\tother `tick` [label](url)\n" +
		"openai models list --transform 'data.#(name==\"`raw`\")'\n"
	got := wrapDescription(source, "   ", 40)
	for _, want := range []string{
		"Use auto.", "- Keep first and second.", "- Read the guide", "https://example.invalid",
		"~~~sh\n   openai responses create --input '`raw` [literal](url)'\n   ```\n   [still literal](url)\n   ~~~",
		"Afterwards, use text.", "````text\n   ```\n   [literal](url)\n   ````",
		"    literal `tick` [label](url)", "\tother `tick` [label](url)",
		"openai models list --transform 'data.#(name==\"`raw`\")'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("wrapped help lost %q:\n%s", want, got)
		}
	}
}

func TestTerminalProseDoesNotNormalizeLiteralCodeTwice(t *testing.T) {
	source := "Keep ``literal `ticks` and [label](url)`` unchanged."
	want := "Keep literal `ticks` and [label](url) unchanged."
	for _, got := range []string{wrapDescription(source, "", 100), wrapPlainDescription(terminalProse(source), "", 100)} {
		if got != want+"\n" {
			t.Fatalf("literal inline code changed during wrapping: %q", got)
		}
	}
}

func TestTerminalProseKeepsMalformedRuns(t *testing.T) {
	for _, source := range []string{strings.Repeat("[", 10000), "`" + strings.Repeat("[malformed](", 1000)} {
		if got := terminalProse(source); got != source {
			t.Fatal("malformed source changed")
		}
	}
}
