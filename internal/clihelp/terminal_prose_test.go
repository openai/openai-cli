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

func TestTerminalProseNormalizesOnlyAuthoredEmphasis(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"tools", "- **Built-in tools**\n- **MCP Tools**\n- **Function calls (custom tools)**", "- Built-in tools\n- MCP Tools\n- Function calls (custom tools)"},
		{"strong", "Use **bold** and **another value**.", "Use bold and another value."},
		{"italic phrase", "Use *two words*.", "Use two words."},
		{"combined", "Use ***strong emphasis***.", "Use strong emphasis."},
		{"underscore prose", "Use _two words_ and __strong words__.", "Use two words and strong words."},
		{"punctuation", "(**Warning:** use **care**.)", "(Warning: use care.)"},
		{"Unicode", "Use **照片** or **图片**.", "Use 照片 or 图片."},
		{"code in emphasis", "Use **`data.*` carefully**.", "Use data.* carefully."},
		{"literal emphasis in code", "Keep `**literal**` and `_tokens_`.", "Keep **literal** and _tokens_."},
		{"literal nested code", "Keep ``*literal* and `ticks` ``.", "Keep *literal* and `ticks` ."},
		{"emphasis in link", "Read [**the guide**](https://example.invalid/**raw**/_id_).", "Read the guide (https://example.invalid/**raw**/_id_)."},
		{"escaped markers", `Keep \**literal\** and \_field\_.`, `Keep \**literal\** and \_field\_.`},
		{"field paths", "Keep data.*.id, foo_bar, data._id_, foo__bar__, and _field_.", "Keep data.*.id, foo_bar, data._id_, foo__bar__, and _field_."},
		{"wildcards", "Keep *.json, **/*.png, *name*, foo*bar*baz, and **name.ext**.", "Keep *.json, **/*.png, *name*, foo*bar*baz, and **name.ext**."},
		{"bare URL", "See https://example.invalid/(**raw**)/_id_?q=*value*.", "See https://example.invalid/(**raw**)/_id_?q=*value*."},
		{"quoted wildcard argument", `Example: --filter '*name*'.`, `Example: --filter '*name*'.`},
		{"quoted strong argument", `Example: --input '**literal** _tokens_'.`, `Example: --input '**literal** _tokens_'.`},
		{"quoted argument phrase", `Example: --input "**literal phrase** _literal phrase_".`, `Example: --input "**literal phrase** _literal phrase_".`},
		{"GJSON wildcard", `Use data.#(name=="*demo*")#.`, `Use data.#(name=="*demo*")#.`},
		{"escaped GJSON wildcard", `Use fav\.movie.#(name=="**demo**")#.`, `Use fav\.movie.#(name=="**demo**")#.`},
		{"quoted code markup", "Keep '--input `raw` [label](url)' literal.", "Keep '--input `raw` [label](url)' literal."},
		{"contractions", "Don't change the user's **chosen value**.", "Don't change the user's chosen value."},
		{"possessive", "Keep users' **chosen values**.", "Keep users' chosen values."},
		{"quoted contraction", "Keep 'don't change **literal**' intact.", "Keep 'don't change **literal**' intact."},
		{"quoted content in emphasis", "**Use '--input **literal**' carefully**.", "Use '--input **literal**' carefully."},
		{"malformed quote", "Keep '--input **unfinished** literal.", "Keep '--input **unfinished** literal."},
		{"URL inside emphasis", "**Read https://example.invalid/**raw** carefully**.", "Read https://example.invalid/**raw** carefully."},
		{"relative link inside emphasis", "**Read [guide](relative/**raw**) carefully**.", "Read guide (relative/**raw**) carefully."},
		{"malformed strong", "Keep **unfinished *span*.", "Keep **unfinished *span*."},
		{"malformed closing", "Keep **unbalanced* or ** spaced **.", "Keep **unbalanced* or ** spaced **."},
		{"empty markers", "Keep ** and __ and ****.", "Keep ** and __ and ****."},
		{"command", "openai responses create --input '**literal** _tokens_'", "openai responses create --input '**literal** _tokens_'"},
		{"indented code", "    **literal** _tokens_ [field](url)", "    **literal** _tokens_ [field](url)"},
		{"fence", "```text\n**literal** _tokens_\n```", "```text\n**literal** _tokens_\n```"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := terminalProse(tc.source); got != tc.want {
				t.Fatalf("terminal prose = %q; want %q", got, tc.want)
			}
		})
	}
}
