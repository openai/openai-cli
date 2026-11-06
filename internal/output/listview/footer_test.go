package listview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestListNavigationUsesCompactPagerControls(t *testing.T) {
	for _, width := range []int{10, 20, 40, 80} {
		m := New(testOptions(t.Context()), nil, func() {}, width, 24)
		m.Update(listPageMessage{page: keyboardPage("id", true)})
		for _, loading := range []bool{false, true} {
			m.loading = loading
			lines := strings.Split(m.View().Content, "\n")
			content := lines[len(lines)-1]
			if !strings.Contains(content, "q") || ansi.StringWidth(content) > width {
				t.Fatalf("width %d: pager controls are not compact: %q", width, content)
			}
			if !loading && width >= 40 && !strings.Contains(content, "Space: browse") {
				t.Fatal("missing navigation keys")
			}
			for _, old := range []string{"p: print", "i: inline", "Switching"} {
				if strings.Contains(content, old) {
					t.Fatal("obsolete pager control", old)
				}
			}
		}
		m.quitting = true
		if m.View().Content != "" || m.View().AltScreen {
			t.Fatal("pager changed screen ownership before restoration")
		}
	}
}
