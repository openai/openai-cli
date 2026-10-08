package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

// These process checks complement scripts/check-list-navigation.py, which sends
// keys through a real PTY. Piped output must retain automatic pagination.
func TestMainListNavigationNoninteractive(t *testing.T) {
	for _, resource := range []struct {
		name, path, object string
		args               []string
	}{
		{"files", "/files", "file", []string{"files", "list"}},
		{"batches", "/batches", "batch", []string{"batches", "list"}},
		{"projects", "/organization/projects", "organization.project", []string{"admin", "organization", "projects", "list"}},
	} {
		for _, mode := range []struct {
			name     string
			flags    []string
			maxItems string
			items    int
			requests int
			raw      bool
		}{
			{name: "default", items: 4, requests: 2},
			{name: "auto", flags: []string{"--format", "auto"}, items: 4, requests: 2},
			{name: "text", flags: []string{"--format", "text"}, items: 4, requests: 2},
			{name: "json", flags: []string{"--format", "json"}, items: 4, requests: 2},
			{name: "jsonl", flags: []string{"--format", "jsonl"}, items: 4, requests: 2},
			{name: "raw", flags: []string{"--format", "raw"}, items: 2, requests: 1, raw: true},
			{name: "extraction", flags: []string{"--transform", "id"}, items: 4, requests: 2},
			{name: "raw extraction", flags: []string{"--transform", "id", "--raw-output"}, items: 4, requests: 2},
			{name: "raw output", flags: []string{"--raw-output"}, items: 4, requests: 2},
			{name: "unlimited", maxItems: "-1", items: 4, requests: 2},
			{name: "zero", maxItems: "0", items: 0, requests: 1},
			{name: "one", maxItems: "1", items: 1, requests: 1},
			{name: "page boundary", maxItems: "2", items: 2, requests: 1},
			{name: "partial second page", maxItems: "3", items: 3, requests: 2},
		} {
			t.Run(resource.name+"/"+mode.name, func(t *testing.T) {
				var mu sync.Mutex
				var cursors []string
				page := func(first int, more bool) string {
					items := []map[string]any{}
					for number := first; number < first+2; number++ {
						items = append(items, map[string]any{"id": fmt.Sprintf("item_%03d", number), "object": resource.object,
							"filename": "synthetic.txt", "name": "Synthetic project", "status": "completed", "purpose": "assistants", "bytes": 5})
					}
					body, err := json.Marshal(map[string]any{"object": "list", "data": items, "has_more": more,
						"first_id": fmt.Sprintf("item_%03d", first), "last_id": fmt.Sprintf("item_%03d", first+1)})
					if err != nil {
						t.Fatal(err)
					}
					return string(body)
				}
				first, second := page(1, true), page(3, false)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					cursor := r.URL.Query().Get("after")
					mu.Lock()
					cursors = append(cursors, cursor)
					mu.Unlock()
					if r.Method != http.MethodGet || r.URL.Path != resource.path || r.URL.Query().Get("limit") != "2" {
						t.Errorf("unexpected synthetic request: %s %s", r.Method, r.URL)
					}
					w.Header().Set("Content-Type", "application/json")
					switch cursor {
					case "":
						io.WriteString(w, first)
					case "item_002":
						io.WriteString(w, second)
					default:
						http.Error(w, "unexpected cursor", http.StatusBadRequest)
					}
				}))
				defer server.Close()
				args := append([]string{"openai"}, mode.flags...)
				args = append(args, resource.args...)
				args = append(args, "--limit", "2")
				if mode.maxItems != "" {
					args = append(args, "--max-items", mode.maxItems)
				}
				got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=" + server.URL,
					"OPENAI_API_KEY=sk-fake-navigation-test", "OPENAI_ADMIN_KEY=sk-fake-navigation-admin", "FORCE_COLOR=0"}, args...)
				mu.Lock()
				actualCursors := slices.Clone(cursors)
				mu.Unlock()
				wantCursors := []string{"", "item_002"}[:mode.requests]
				if got.code != 0 || got.stderr != "" || !slices.Equal(actualCursors, wantCursors) {
					t.Fatalf("result=%+v cursors=%q; want %q", got, actualCursors, wantCursors)
				}
				if strings.Contains(got.stdout, "Space: more") || strings.Contains(got.stdout, "\x1b") {
					t.Fatalf("noninteractive output entered navigation: %q", got.stdout)
				}
				if mode.raw {
					if got.stdout != first+"\n" {
						t.Fatalf("raw page changed: %q", got.stdout)
					}
					return
				}
				for number := 1; number <= 4; number++ {
					want := 0
					if number <= mode.items {
						want = 1
					}
					if count := strings.Count(got.stdout, fmt.Sprintf("item_%03d", number)); count != want {
						t.Errorf("item %d appears %d times, want %d: %q", number, count, want, got.stdout)
					}
				}
			})
		}
	}
}
