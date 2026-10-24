package app

import (
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/output/listview"
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
		opts := ShowJSONOpts{Context: t.Context(), Operation: "(resource) models > (method) list", OutputKind: OutputPageItem}
		viewer := listViewerOptions(opts)
		viewer.RenderPage = func([]gjson.Result, int) (string, error) { return "first\nsecond\nthird\nfourth\nfifth\nsixth", nil }
		fetch := func() (listview.Page, error) { return listview.Page{Items: make([]gjson.Result, test.count)}, nil }
		m := listview.New(viewer, fetch, func() {}, test.width, 5)
		m.Update(m.Init()())
		content := m.View().Content
		lines := strings.Split(content, "\n")
		if got := lines[len(lines)-2]; got != test.want {
			t.Errorf("width %d: print hint = %q, want %q", test.width, got, test.want)
		}
		opts.Operation = "(resource) files > (method) list"
		viewer = listViewerOptions(opts)
		viewer.RenderPage = func([]gjson.Result, int) (string, error) { return "first\nsecond\nthird\nfourth\nfifth\nsixth", nil }
		m = listview.New(viewer, fetch, func() {}, 80, 5)
		m.Update(m.Init()())
		if !strings.Contains(m.View().Content, "p: print page, quit") {
			t.Fatal("models hint changed another resource")
		}
	}
}

func loadedListModel(opts ShowJSONOpts, items []gjson.Result, more bool, width, height int) *listview.Model {
	m := listview.New(listViewerOptions(opts), func() (listview.Page, error) { return listview.Page{Items: items, More: more}, nil }, func() {}, width, height)
	m.Update(m.Init()())
	return m
}
