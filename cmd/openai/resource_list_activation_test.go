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
	{"projects-full", "/organization/projects", "organization.project", []string{"admin", "organization", "projects", "list", "--include-archived"}, []string{"NAME", "STATUS", "ID"}},
	{"projects-admin", "/organization/projects", "organization.project", []string{"admin", "projects", "list", "--include-archived"}, []string{"NAME", "STATUS", "ID"}},
	{"projects-short", "/organization/projects", "organization.project", []string{"projects", "list", "--include-archived"}, []string{"NAME", "STATUS", "ID"}},
	{"projects-legacy", "/organization/projects", "organization.project", []string{"admin:organization:projects", "list", "--include-archived"}, []string{"NAME", "STATUS", "ID"}},
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

func fixtureListCursor(route resourceListRoute) string {
	switch route.object {
	case "organization.project":
		return "project-envelope-cursor" // ConversationCursorPage reads last_id.
	case "group":
		return "opaque:02/+?" // NextCursorPage reads next, including URL characters.
	default:
		return "item_002" // CursorPage reads the last row ID.
	}
}

func newResourceListFixture(t *testing.T, route resourceListRoute, mode string) *resourceListFixture {
	t.Helper()
	fixture := &resourceListFixture{}
	nextCursor := fixtureListCursor(route)
	if mode == "repeat-from-start" {
		nextCursor = "seed" // resourceListArgs sends --after seed on the first request.
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
		if r.Header.Get("X-List-Test") != "preserved" || r.Header.Get("OpenAI-Organization") != "org-synthetic" || r.Header.Get("OpenAI-Project") != "proj-synthetic" {
			t.Error("list request discarded a header or its organization/project context")
		}
		key := "sk-fake-list-override"
		if strings.HasPrefix(route.path, "/organization/") {
			key = "sk-fake-list-admin-override"
		}
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("list request used the wrong credential")
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
		if mode != "empty" && mode != "empty-continuation" && !(mode == "empty-next-continuation" && request == 2) {
			for number := (request-1)*2 + 1; number <= request*2; number++ {
				item := map[string]any{"id": fmt.Sprintf("item_%03d", number), "object": route.object}
				if route.object == "file" && number == request*2 && (mode == "repeat-from-start" || mode == "repeat-cursor" && request == 2) {
					item["id"] = nextCursor // The Files SDK derives its cursor from the last row.
				}
				switch route.object {
				case "file":
					item["filename"], item["purpose"], item["bytes"], item["status"] = "synthetic.jsonl", "assistants", 1024, "processed"
				case "batch":
					item["status"] = "completed"
				case "organization.project":
					item["name"], item["status"] = "Synthetic project", "active"
					item["psp_decryption_key_id"] = nil
				case "webhook_endpoint":
					item["name"], item["event_types"], item["url"] = "Synthetic hook", []string{"batch.failed"}, "https://example.test/webhook"
				case "skill":
					item["name"], item["default_version"], item["latest_version"] = "Synthetic skill", "1", "2"
				case "group":
					delete(item, "object") // Organization groups do not use an object discriminator.
					item["name"], item["group_type"], item["is_scim_managed"] = "Synthetic group", "scim", true
				}
				if mode == "unfamiliar" {
					item["unfamiliar"] = "retain-synthetic-detail"
				}
				if mode == "missing-cursor" && route.object == "skill" {
					delete(item, "id")
				}
				items = append(items, item)
			}
		}
		more := request == 1 && mode != "complete" && mode != "empty" && mode != "unfamiliar"
		envelope := map[string]any{"object": "list", "data": items, "has_more": more, "first_id": "item_001", "last_id": nextCursor}
		if route.object == "group" && mode != "missing-cursor" {
			envelope["next"] = nextCursor
		}
		if (mode == "repeat-cursor" || mode == "empty-next-continuation") && request == 2 {
			envelope["has_more"] = true
		}
		if route.object == "organization.project" && (mode == "missing-cursor" || mode == "missing-legacy-cursor") {
			delete(envelope, "last_id")
		}
		if request == 1 && (mode == "legacy" || mode == "missing-legacy-cursor") {
			delete(envelope, "has_more")
		}
		_ = json.NewEncoder(w).Encode(envelope)
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func resourceListArgs(route resourceListRoute, extra ...string) []string {
	args := []string{"openai", "--api-key", "sk-fake-list-override", "--admin-api-key", "sk-fake-list-admin-override",
		"--organization", "org-synthetic", "--project", "proj-synthetic", "--header", "X-List-Test: preserved"}
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
		want = append(want, fixtureListCursor(route))
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

func TestMainPipedListCursorFailures(t *testing.T) {
	files := resourceListRoutes[0]
	projects := resourceListRoutes[2]
	groups := resourceListRoute{name: "groups", path: "/organization/groups", object: "group",
		args: []string{"admin", "organization", "groups", "list"}}
	for _, tc := range []struct {
		name, mode string
		route      resourceListRoute
		flags      []string
		calls      int
	}{
		{"files-default", "repeat-cursor", files, nil, 2},
		{"files-repeat-first-after", "repeat-from-start", files, []string{"--format", "json"}, 1},
		{"groups-repeat-first-after", "repeat-from-start", groups, []string{"--format", "jsonl"}, 1},
		{"projects-alias-repeat-first-after", "repeat-from-start", resourceListRoutes[4], []string{"--format", "json"}, 1},
		{"files-json", "repeat-cursor", files, []string{"--format", "json", "--max-items", "-1"}, 2},
		{"groups-jsonl", "repeat-cursor", groups, []string{"--format", "jsonl"}, 2},
		{"projects-extract", "repeat-cursor", projects, []string{"--transform", "id", "--raw-output"}, 2},
		{"groups-missing-next", "missing-cursor", groups, []string{"--format", "json"}, 1},
		{"projects-missing-last-id", "missing-cursor", projects, nil, 1},
		{"files-empty-with-continuation", "empty-continuation", files, nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newResourceListFixture(t, tc.route, tc.mode)
			got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=" + fixture.server.URL + "/v1"},
				resourceListArgs(tc.route, append(tc.flags, "--format-error", "json")...)...)
			fixture.assertCursors(t, tc.route, tc.calls)
			require.Equal(t, 1, got.code, got.stderr)
			diagnostic := decodeMainStructuredError(t, "json", got.stderr)
			require.Equal(t, map[string]any{"message": "List pagination stopped because the API returned a missing or repeated cursor. Output may be incomplete."}, diagnostic)
			if tc.mode != "empty-continuation" {
				require.Contains(t, got.stdout, "item_001", "preserve the first page on failure")
			}
			if slices.Contains(tc.flags, "json") {
				require.True(t, strings.HasPrefix(strings.TrimSpace(got.stdout), "["))
				require.False(t, json.Valid([]byte(got.stdout)), "a partial list must not claim to be complete")
			}
		})
	}
}

func TestMainPipedListLimitsStopBeforeAStalledCursor(t *testing.T) {
	files := resourceListRoutes[0]
	for _, tc := range []struct {
		format, maximum string
		items           int
	}{
		{"json", "0", 0},
		{"json", "1", 1},
		{"json", "2", 2},
		{"raw", "0", 2}, // Raw returns the original response envelope.
	} {
		t.Run(tc.format+"/"+tc.maximum, func(t *testing.T) {
			fixture := newResourceListFixture(t, files, "repeat-from-start")
			got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=" + fixture.server.URL + "/v1"},
				resourceListArgs(files, "--format", tc.format, "--max-items", tc.maximum)...)
			require.Equal(t, 0, got.code, got.stderr)
			var items []map[string]any
			if tc.format == "raw" {
				var result struct {
					Data    []map[string]any
					HasMore bool `json:"has_more"`
				}
				require.NoError(t, json.Unmarshal([]byte(got.stdout), &result))
				require.True(t, result.HasMore)
				items = result.Data
			} else {
				require.NoError(t, json.Unmarshal([]byte(got.stdout), &items))
				require.NotNil(t, items, "finite JSON output must be an array, including at zero items")
			}
			require.Len(t, items, tc.items)
			if tc.items > 0 {
				require.Equal(t, "item_001", items[0]["id"])
			}
			fixture.assertCursors(t, files, 1)
		})
	}
}
