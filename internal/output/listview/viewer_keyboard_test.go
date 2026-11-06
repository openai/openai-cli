package listview

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/tidwall/gjson"
)

func keyboardPage(prefix string, more bool) Page {
	items := make([]gjson.Result, 20)
	for i := range items {
		items[i] = gjson.Parse(fmt.Sprintf(`{"id":"%s-%02d"}`, prefix, i))
	}
	return Page{Items: items, More: more}
}

var viewerKeyPairs = []struct {
	name          string
	forward, back tea.KeyPressMsg
}{
	{"arrows", tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyUp}},
	{"vim", tea.KeyPressMsg{Code: 'j', Text: "j"}, tea.KeyPressMsg{Code: 'k', Text: "k"}},
	{"half_page", tea.KeyPressMsg{Code: 'd', Text: "d"}, tea.KeyPressMsg{Code: 'u', Text: "u"}},
	{"control_half_page", tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}},
	{"page_keys", tea.KeyPressMsg{Code: tea.KeyPgDown}, tea.KeyPressMsg{Code: tea.KeyPgUp}},
	{"f_b", tea.KeyPressMsg{Code: 'f', Text: "f"}, tea.KeyPressMsg{Code: 'b', Text: "b"}},
	{"space_b", tea.KeyPressMsg{Code: ' ', Text: " "}, tea.KeyPressMsg{Code: 'b', Text: "b"}},
}

func TestViewerPreservesViewportKeyboardBindings(t *testing.T) {
	for _, test := range viewerKeyPairs {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			m := New(viewerOptions(t, &out), nil, func() {}, 80, 8)
			m.Update(listPageMessage{page: keyboardPage("first", true)})
			initial := m.records.screen.content
			if !strings.HasPrefix(initial, "first-00") {
				t.Fatal("fixture did not start at its first record", initial)
			}
			cmd := viewerPress(m, test.forward)
			if cmd != nil || strings.HasPrefix(m.View().Content, "first-00") {
				t.Fatalf("%s did not scroll loaded records: %q", test.forward.String(), m.View().Content)
			}
			cmd = viewerPress(m, test.back)
			if cmd != nil || m.records.screen.content != initial {
				t.Fatalf("%s did not return to the original records: %q", test.back.String(), m.View().Content)
			}
			m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
			if err := m.Finish(nil, nil); err != nil || out.Len() != 0 {
				t.Fatal("navigation changed successful quit output", err, out.String())
			}
		})
	}
}

func TestViewerPageKeysPreserveLazyCachedNavigation(t *testing.T) {
	for _, test := range viewerKeyPairs {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			var requests atomic.Int32
			opts := viewerOptions(t, &out)
			m := New(opts, func() (Page, error) {
				page := keyboardPage("second", false)
				if requests.Add(1) == 1 {
					page = keyboardPage("first", true)
				}
				return page, nil
			}, func() {}, 80, 8)
			m.Update(m.Init()())
			// One forward command can fill its screen across the API boundary.
			// Until that move needs a row beyond the first response, stay local.
			var next tea.Cmd
			for steps := 0; next == nil && steps < 24; steps++ {
				next = viewerPress(m, test.forward)
				if next != nil && !strings.Contains(m.View().Content, "first-19") {
					t.Fatal("another page was requested before reaching the last loaded row", m.View().Content)
				}
			}
			if next == nil || requests.Load() > 2 {
				t.Fatalf("%s did not fetch the next page once", test.forward.String())
			}
			m.Update(next())
			for steps := 0; m.records.screen.end.item < 40 && steps < 24; steps++ {
				if cmd := viewerPress(m, test.forward); cmd != nil {
					t.Fatal("fetched after the final API page")
				}
			}
			if !strings.Contains(m.View().Content, "second-19") || requests.Load() != 2 {
				t.Fatal("the final result was not reachable", m.View().Content)
			}
			for steps := 0; m.records.screen.from.item >= 20 && steps < 24; steps++ {
				if cmd := viewerPress(m, test.back); cmd != nil {
					t.Fatal("back navigation fetched data")
				}
			}
			if !strings.Contains(m.View().Content, "first-") {
				t.Fatal("back navigation lost cached records", m.View().Content)
			}
			if cmd := viewerPress(m, test.forward); cmd != nil || requests.Load() != 2 || !strings.Contains(m.View().Content, "second-") {
				t.Fatal("cached navigation changed the fetch frontier")
			}
			m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
			if err := m.Finish(nil, nil); err != nil || out.Len() != 0 {
				t.Fatal("page keys changed successful quit output", err, out.String())
			}
		})
	}
}

func TestViewerLineKeysUseLoadedRowsFirst(t *testing.T) {
	var out bytes.Buffer
	m := New(viewerOptions(t, &out), func() (Page, error) {
		return viewerPage("second"), nil
	}, func() {}, 80, 8)
	page := keyboardPage("first", true)
	m.Update(listPageMessage{page: page})
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyDown}, {Code: 'j', Text: "j"}, {Code: 'd', Text: "d"}, {Code: 'd', Mod: tea.ModCtrl},
	} {
		if _, next := m.Update(key); next != nil || m.loading {
			t.Fatalf("%s requested another API page", key.String())
		}
	}
}

func TestViewerKeyboardWhileLoadingKeepsCancellation(t *testing.T) {
	var out bytes.Buffer
	var requests atomic.Int32
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	m := New(viewerOptions(t, &out), func() (Page, error) {
		requests.Add(1)
		close(started)
		<-ctx.Done()
		return Page{}, ctx.Err()
	}, cancel, 80, 8)
	m.Update(listPageMessage{page: keyboardPage("first", true)})
	var pending tea.Cmd
	for steps := 0; pending == nil && steps < 20; steps++ {
		pending = viewerPress(m, tea.KeyPressMsg{Code: ' ', Text: " "})
	}
	if pending == nil {
		t.Fatal("fixture did not start its next page")
	}
	<-started
	view := m.View().Content
	for _, keys := range viewerKeyPairs {
		for _, msg := range []tea.KeyPressMsg{keys.forward, keys.back} {
			if _, cmd := m.Update(msg); cmd != nil || m.View().Content != view {
				t.Fatalf("%s changed navigation during the pending request", msg.String())
			}
		}
	}
	if requests.Load() != 1 {
		t.Fatal("navigation duplicated the pending request", requests.Load())
	}
	m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	m.Update(pending())
	if err := m.Finish(nil, nil); err != nil || out.Len() != 0 {
		t.Fatal("quit did not cancel cleanly after navigation", err, out.String())
	}
}
