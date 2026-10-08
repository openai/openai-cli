package custom

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	"github.com/tidwall/gjson"
)

func TestModelsListPrintHintExplainsAllSelectedRecords(t *testing.T) {
	for _, test := range []struct {
		count int
		width int
		want  string
	}{
		{11892, 80, "p: print all 11892 records, quit"},
		{11892, 24, "p: all 11892, quit"},
		{11892, 12, "p: all 11892"},
		{11892, 10, "p: all"},
		{1, 80, "p: print 1 record, quit"},
		{1, 12, "p: all 1"},
	} {
		m := &listNavigation{
			opts:     ShowJSONOpts{Operation: "(resource) models > (method) list", OutputKind: OutputPageItem},
			viewport: viewport.New(viewport.WithWidth(test.width), viewport.WithHeight(3)),
			pages:    []listNavigationPage{{items: make([]gjson.Result, test.count)}},
		}
		m.viewport.SetContent("first\nsecond\nthird\nfourth")
		content := m.View().Content
		lines := strings.Split(content, "\n")
		if got := lines[len(lines)-2]; got != test.want {
			t.Errorf("width %d: print hint = %q, want %q", test.width, got, test.want)
		}
		m.opts.Operation = "(resource) files > (method) list"
		m.viewport.SetWidth(80)
		if !strings.Contains(m.View().Content, "p: print page, quit") {
			t.Fatal("models hint changed another resource")
		}
	}
}

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
		{name: "minimum height exact fit", content: "model-first\n", width: 80, height: 1},
		{name: "minimum height overflow", content: "model-first\nmodel-second\n", width: 80, height: 1, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := modelsListNeedsViewport(test.content, test.width, test.height); got != test.want {
				t.Fatalf("modelsListNeedsViewport(%q, %d, %d) = %v, want %v", test.content, test.width, test.height, got, test.want)
			}
		})
	}
}

func TestModelsListViewportFooterTracksVisiblePosition(t *testing.T) {
	m := &listNavigation{
		viewport: viewport.New(viewport.WithWidth(80), viewport.WithHeight(3)),
		pages:    []listNavigationPage{{more: false}},
	}
	m.viewport.SoftWrap = false
	m.viewport.SetContent("first\nsecond\nthird\nfourth\nfifth\nsixth")

	checkFooter := func(wantEnd bool) {
		t.Helper()
		content := m.View().Content
		if got := strings.Contains(content, "End of results"); got != wantEnd {
			t.Fatalf("end hint = %v, want %v: %q", got, wantEnd, content)
		}
		if !wantEnd && !strings.Contains(content, "Space: more") {
			t.Fatalf("loaded rows below the viewport lost their navigation hint: %q", content)
		}
		for _, hint := range []string{"b: back", "q: quit", "p: print page, quit"} {
			if !strings.Contains(content, hint) {
				t.Fatalf("footer lost %q: %q", hint, content)
			}
		}
	}

	if m.viewport.AtBottom() {
		t.Fatal("test requires rows below the initial viewport")
	}
	checkFooter(false)
	m.viewport.GotoBottom()
	checkFooter(true)
	m.viewport.GotoTop()
	checkFooter(false)

	// A screenful is also the end when the loaded response has no more items.
	m.viewport.SetContent("first\nsecond\nthird")
	checkFooter(true)
}

func TestModelsListViewportFooterPreservesMorePages(t *testing.T) {
	m := &listNavigation{
		viewport: viewport.New(viewport.WithWidth(80), viewport.WithHeight(3)),
		pages:    []listNavigationPage{{more: true}},
	}
	m.viewport.SetContent("first")
	if !m.viewport.AtBottom() {
		t.Fatal("test requires the bottom of a loaded page")
	}
	content := m.View().Content
	if strings.Contains(content, "End of results") || !strings.Contains(content, "Space: more") {
		t.Fatalf("pending API pages lost their navigation hint: %q", content)
	}
}
