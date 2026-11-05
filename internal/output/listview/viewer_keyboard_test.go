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

func keyboardOptions(t *testing.T, out *bytes.Buffer) Options {
	return Options{Context: t.Context(), Stdout: out, RenderPage: func(items []gjson.Result, _ int) (string, error) {
		var text strings.Builder
		for _, item := range items {
			fmt.Fprintln(&text, item.Get("id").String())
		}
		return text.String(), nil
	}}
}

func keyboardPage(prefix string, more bool) Page {
	items := make([]gjson.Result, 20)
	for i := range items {
		items[i] = gjson.Parse(fmt.Sprintf(`{"id":"%s-%02d"}`, prefix, i))
	}
	return Page{Items: items, More: more}
}

func TestViewerPreservesViewportKeyboardBindings(t *testing.T) {
	for _, test := range []struct {
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
	} {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			m := New(keyboardOptions(t, &out), nil, func() {}, 80, 8)
			m.Update(listPageMessage{page: keyboardPage("first", true)})
			initial := m.View().Content
			if !strings.HasPrefix(initial, "first-00") {
				t.Fatal("fixture did not start at its first record", initial)
			}
			_, cmd := m.Update(test.forward)
			if cmd != nil || strings.HasPrefix(m.View().Content, "first-00") {
				t.Fatalf("%s did not scroll loaded records: %q", test.forward.String(), m.View().Content)
			}
			_, cmd = m.Update(test.back)
			if cmd != nil || m.View().Content != initial {
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
	for _, test := range []struct {
		name          string
		local         bool
		forward, back tea.KeyPressMsg
	}{
		{"page_keys", false, tea.KeyPressMsg{Code: tea.KeyPgDown}, tea.KeyPressMsg{Code: tea.KeyPgUp}},
		{"f_b", false, tea.KeyPressMsg{Code: 'f', Text: "f"}, tea.KeyPressMsg{Code: 'b', Text: "b"}},
		{"space_b", false, tea.KeyPressMsg{Code: ' ', Text: " "}, tea.KeyPressMsg{Code: 'b', Text: "b"}},
		{"local_arrows", true, tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyUp}},
		{"local_vim", true, tea.KeyPressMsg{Code: 'j', Text: "j"}, tea.KeyPressMsg{Code: 'k', Text: "k"}},
		{"local_half_page", true, tea.KeyPressMsg{Code: 'd', Text: "d"}, tea.KeyPressMsg{Code: 'u', Text: "u"}},
		{"local_control_half_page", true, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			var requests atomic.Int32
			opts := keyboardOptions(t, &out)
			m := New(opts, func() (Page, error) {
				page := keyboardPage("second", false)
				if requests.Add(1) == 1 {
					page = keyboardPage("first", true)
				}
				page.Local = test.local
				return page, nil
			}, func() {}, 80, 8)
			m.Update(m.Init()())
			var next tea.Cmd
			for steps := 0; next == nil && steps < 20; steps++ {
				atEnd := strings.Contains(m.View().Content, "first-19")
				_, next = m.Update(test.forward)
				if !atEnd && next != nil {
					t.Fatal("requested another page before displaying loaded records")
				}
			}
			if next == nil {
				t.Fatalf("%s did not reach the next API page", test.forward.String())
			}
			m.Update(next())
			if requests.Load() != 2 || !strings.HasPrefix(m.View().Content, "second-00") {
				t.Fatal("next page did not start at its first record", requests.Load(), m.View().Content)
			}
			for steps := 0; steps < 20; steps++ {
				if _, cmd := m.Update(test.forward); cmd != nil {
					t.Fatal("fetched after the final API page")
				}
			}
			for steps := 0; !strings.Contains(m.View().Content, "first-") && steps < 20; steps++ {
				if _, cmd := m.Update(test.back); cmd != nil {
					t.Fatal("back navigation fetched data")
				}
			}
			if !strings.Contains(m.View().Content, "first-19") || requests.Load() != 2 {
				t.Fatal("back navigation lost the cached page", requests.Load(), m.View().Content)
			}
			if _, cmd := m.Update(test.forward); cmd != nil || !strings.HasPrefix(m.View().Content, "second-00") {
				t.Fatal("forward navigation did not reuse the cached next page")
			}
			if requests.Load() != 2 {
				t.Fatal("cached navigation changed the fetch frontier", requests.Load())
			}
			m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
			if err := m.Finish(nil, nil); err != nil || out.Len() != 0 {
				t.Fatal("page keys changed successful quit output", err, out.String())
			}
		})
	}
}

func TestViewerLineKeysDoNotRequestAnotherAPIPage(t *testing.T) {
	var out bytes.Buffer
	m := New(keyboardOptions(t, &out), func() (Page, error) {
		return viewerPage("second"), nil
	}, func() {}, 80, 8)
	page := viewerPage("first")
	page.More = true
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
	m := New(keyboardOptions(t, &out), func() (Page, error) {
		requests.Add(1)
		close(started)
		<-ctx.Done()
		return Page{}, ctx.Err()
	}, cancel, 80, 8)
	m.Update(listPageMessage{page: keyboardPage("first", true)})
	var pending tea.Cmd
	for steps := 0; pending == nil && steps < 20; steps++ {
		_, pending = m.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	}
	if pending == nil {
		t.Fatal("fixture did not start its next page")
	}
	<-started
	view := m.View().Content
	for _, msg := range []tea.KeyPressMsg{
		{Code: tea.KeyDown}, {Code: tea.KeyUp}, {Code: tea.KeyPgDown}, {Code: tea.KeyPgUp},
		{Code: 'j', Text: "j"}, {Code: 'k', Text: "k"}, {Code: 'd', Text: "d"}, {Code: 'u', Text: "u"},
		{Code: 'd', Mod: tea.ModCtrl}, {Code: 'u', Mod: tea.ModCtrl},
		{Code: 'f', Text: "f"}, {Code: 'b', Text: "b"}, {Code: ' ', Text: " "},
	} {
		if _, cmd := m.Update(msg); cmd != nil || m.View().Content != view {
			t.Fatalf("%s changed navigation during the pending request", msg.String())
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
