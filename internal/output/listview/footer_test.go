package listview

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
)

func TestModelsListViewportFooterTracksVisiblePosition(t *testing.T) {
	m := &Model{
		viewport: viewport.New(viewport.WithWidth(80), viewport.WithHeight(3)),
		pages:    []Page{{More: false}},
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
	m := &Model{
		viewport: viewport.New(viewport.WithWidth(80), viewport.WithHeight(3)),
		pages:    []Page{{More: true}},
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
