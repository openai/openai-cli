package listview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestListNavigationUsesCompactPagerControls(t *testing.T) {
	for _, width := range []int{10, 20, 40, 80} {
		m := New(Options{}, nil, func() {}, width, 24)
		m.more = true
		for _, loading := range []bool{false, true} {
			m.loading = loading
			content := m.View().Content
			if strings.Contains(content, "\n") || !strings.Contains(content, "q") || ansi.StringWidth(content) > width {
				t.Fatalf("width %d: pager controls are not compact: %q", width, content)
			}
			if !loading && (!strings.Contains(content, "Space") || !strings.Contains(content, "b")) {
				t.Fatal("missing navigation keys")
			}
			for _, old := range []string{"p: print", "i: inline", "Switching"} {
				if strings.Contains(content, old) {
					t.Fatal("obsolete pager control", old)
				}
			}
		}
		m.quitting = true
		if m.View().Content != "" || !m.View().AltScreen {
			t.Fatal("pager changed screen ownership before restoration")
		}
	}
}
