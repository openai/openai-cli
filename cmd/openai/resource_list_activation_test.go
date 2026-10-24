package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type resourceListRoute struct {
	name, path, object string
	args, headers      []string
}

var resourceListRoutes = []resourceListRoute{
	{"files", "/files", "file", []string{"files", "list", "--purpose", "assistants", "--order", "asc"}, []string{"ID", "FILENAME", "PURPOSE", "SIZE", "STATUS"}},
	{"batches", "/batches", "batch", []string{"batches", "list"}, []string{"ID", "STATUS"}},
	{"projects-full", "/organization/projects", "organization.project", []string{"admin", "organization", "projects", "list", "--include-archived"}, []string{"ID", "NAME", "STATUS"}},
	{"projects-admin", "/organization/projects", "organization.project", []string{"admin", "projects", "list", "--include-archived"}, []string{"ID", "NAME", "STATUS"}},
	{"projects-short", "/organization/projects", "organization.project", []string{"projects", "list", "--include-archived"}, []string{"ID", "NAME", "STATUS"}},
	{"projects-legacy", "/organization/projects", "organization.project", []string{"admin:organization:projects", "list", "--include-archived"}, []string{"ID", "NAME", "STATUS"}},
}

// The state endpoint belongs to the observer, never the CLI. It lets the PTY
// checks assert real request boundaries rather than infer them from rendering.
type resourceListFixture struct {
	mu           sync.Mutex
	cursors      []string
	inflight     int
	maximum      int
	disconnected int
	server       *httptest.Server
}

func newResourceListFixture(t *testing.T, route resourceListRoute, mode string) *resourceListFixture {
	t.Helper()
	fixture := &resourceListFixture{}
	nextCursor := "item_002"
	if route.object == "organization.project" {
		// ConversationCursorPage must use the envelope cursor, not the row ID.
		nextCursor = "project-envelope-cursor"
	}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_state" {
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"requests": len(fixture.cursors), "inflight": fixture.inflight,
				"maximum": fixture.maximum, "disconnected": fixture.disconnected})
			return
		}
		fixture.mu.Lock()
		fixture.cursors = append(fixture.cursors, r.URL.Query().Get("after"))
		request := len(fixture.cursors)
		fixture.inflight++
		fixture.maximum = max(fixture.maximum, fixture.inflight)
		fixture.mu.Unlock()
		defer func() { fixture.mu.Lock(); fixture.inflight--; fixture.mu.Unlock() }()
		query := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != "/v1"+route.path || query.Get("limit") != "2" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		wantCursor := "seed"
		if request > 1 {
			wantCursor = nextCursor
		}
		if query.Get("after") != wantCursor {
			t.Errorf("request %d cursor=%q, want %q", request, query.Get("after"), wantCursor)
		}
		if r.Header.Get("X-List-Test") != "preserved" || r.Header.Get("OpenAI-Organization") != "org-synthetic" {
			t.Error("list request discarded the explicit header or organization")
		}
		if route.object == "file" && (query.Get("purpose") != "assistants" || query.Get("order") != "asc") {
			t.Errorf("file query changed: %v", query)
		}
		if route.object == "organization.project" && query.Get("include_archived") != "true" {
			t.Errorf("project query changed: %v", query)
		}
		if mode == "blocked-first" || mode == "blocked-next" && request == 2 {
			<-r.Context().Done()
			fixture.mu.Lock()
			fixture.disconnected++
			fixture.mu.Unlock()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if mode == "error-first" || mode == "error-next" && request == 2 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"synthetic list failure","type":"invalid_request_error"}}`))
			return
		}
		items := []map[string]any{}
		if mode != "empty" {
			for number := (request-1)*2 + 1; number <= request*2; number++ {
				item := map[string]any{"id": fmt.Sprintf("item_%03d", number), "object": route.object}
				switch route.object {
				case "file":
					item["filename"], item["purpose"], item["bytes"], item["status"] = "synthetic.jsonl", "assistants", 1024, "processed"
				case "batch":
					item["status"] = "completed"
				case "organization.project":
					item["name"], item["status"] = "Synthetic project", "active"
				}
				if mode == "unfamiliar" {
					item["unfamiliar"] = "retain-synthetic-detail"
				}
				items = append(items, item)
			}
		}
		more := request == 1 && mode != "complete" && mode != "empty" && mode != "unfamiliar"
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": items, "has_more": more,
			"first_id": "item_001", "last_id": nextCursor})
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func resourceListArgs(route resourceListRoute, extra ...string) []string {
	args := []string{"openai", "--organization", "org-synthetic", "--header", "X-List-Test: preserved"}
	args = append(args, route.args...)
	args = append(args, "--limit", "2", "--after", "seed")
	return append(args, extra...)
}

func (fixture *resourceListFixture) assertCursors(t *testing.T, route resourceListRoute, count int) {
	t.Helper()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	want := []string{"seed"}
	if count == 2 {
		cursor := "item_002"
		if route.object == "organization.project" {
			cursor = "project-envelope-cursor"
		}
		want = append(want, cursor)
	}
	require.Equal(t, want, fixture.cursors)
	require.Equal(t, 1, fixture.maximum, "list requests must remain serial")
}

func TestMainResourceListActivationJSONAliases(t *testing.T) {
	for _, route := range resourceListRoutes {
		t.Run(route.name, func(t *testing.T) {
			fixture := newResourceListFixture(t, route, "normal")
			got := runMainDispatchWithEnv(t, "bash", []string{
				"OPENAI_BASE_URL=" + fixture.server.URL + "/v1", "OPENAI_API_KEY=sk-fake-resource-list",
				"OPENAI_ADMIN_KEY=sk-fake-resource-list-admin", "FORCE_COLOR=0",
			}, resourceListArgs(route, "--format", "json")...)
			require.Equal(t, 0, got.code, got.stderr)
			require.Empty(t, got.stderr)
			var rows []map[string]any
			require.NoError(t, json.Unmarshal([]byte(got.stdout), &rows), "finite JSON must remain one array")
			require.Len(t, rows, 4)
			for index, row := range rows {
				require.Equal(t, fmt.Sprintf("item_%03d", index+1), row["id"])
				require.Equal(t, route.object, row["object"])
			}
			fixture.assertCursors(t, route, 2)
		})
	}
}

func TestMainResourceListActivationPipe(t *testing.T) {
	route := resourceListRoutes[0]
	for _, tc := range []struct {
		name, mode  string
		flags       []string
		code, calls int
	}{
		{"default", "normal", nil, 0, 2},
		{"raw-envelope", "normal", []string{"--format", "raw"}, 0, 1},
		{"zero-error", "error-first", []string{"--max-items", "0", "--format-error", "json"}, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newResourceListFixture(t, route, tc.mode)
			got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=" + fixture.server.URL + "/v1",
				"OPENAI_API_KEY=sk-fake-resource-list", "FORCE_COLOR=0"}, resourceListArgs(route, tc.flags...)...)
			require.Equal(t, tc.code, got.code, got.stderr)
			require.NotContains(t, got.stdout, "\x1b")
			require.NotContains(t, got.stdout, "q: quit")
			if tc.code != 0 {
				require.Empty(t, got.stdout)
				require.Contains(t, got.stderr, "synthetic list failure")
			} else if slices.Contains(tc.flags, "raw") {
				var envelope struct {
					Data    []json.RawMessage `json:"data"`
					HasMore bool              `json:"has_more"`
				}
				require.NoError(t, json.Unmarshal([]byte(got.stdout), &envelope))
				require.Len(t, envelope.Data, 2)
				require.True(t, envelope.HasMore)
			} else {
				for number := 1; number <= 4; number++ {
					require.Equal(t, 1, strings.Count(got.stdout, fmt.Sprintf("item_%03d", number)))
				}
			}
			fixture.assertCursors(t, route, tc.calls)
		})
	}
}
