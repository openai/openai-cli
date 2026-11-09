//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMainRegisteredListPageSizes(t *testing.T) {
	for _, tc := range []struct {
		command, route, query string
		height                int
	}{
		{"skills list --browse", "skills", "limit=7", 9},
		{"batches list --pager", "batches", "limit=100", 103},
		{"chat completions list --pager", "chat/completions", "limit=148", 150},
		{"chat completions messages list chat_example --pager", "chat/completions/chat_example/messages", "limit=148", 150},
		{"admin groups list --pager", "organization/groups", "limit=1000", 1004},
		{"projects groups list proj_example --pager", "organization/projects/proj_example/groups", "limit=100", 150},
		{"projects rate-limits list-rate-limits proj_example --pager", "organization/projects/proj_example/rate_limits", "limit=1000", 1004},
		{"admin:organization:roles list --pager", "organization/roles", "limit=148", 150},
		{"skills list --pager --limit 4", "skills", "limit=4", 19},
		{"skills list --pager --max-items 3", "skills", "limit=3", 19},
		{"skills list --browse=false", "skills", "", 19},
		{"skills list --format json", "skills", "", 19},
		{"widgets list-limit --pager", "containers", "limit=7", 9},
		{"widgets list-nullable --pager", "containers", "limit=7", 9},
		{"widgets list-nullable --pager --limit null", "containers", "limit=", 9},
		{"widgets list-page-size --pager", "containers", "page_size=7", 9},
		{"widgets list-page-size --pager -n 3", "containers", "page_size=3", 9},
		{"widgets list-per-page --pager", "containers", "per_page=7", 9},
		{"widgets list-local-limit --pager", "containers", "", 9},
		{"widgets list-unpaged --pager", "containers", "", 9},
	} {
		t.Run(tc.command, func(t *testing.T) {
			var calls, lookups atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/webhook_event_types" {
					lookups.Add(1)
					if r.Method != http.MethodGet || r.URL.RawQuery != "" {
						t.Errorf("static catalog lookup received collection options: %s %s", r.Method, r.URL)
					}
					io.WriteString(w, `{"object":"list","data":["batch.completed"]}`)
					return
				}
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/v1/"+tc.route || r.URL.RawQuery != tc.query {
					t.Errorf("got %s %s, want GET /v1/%s with query %q", r.Method, r.URL, tc.route, tc.query)
				}
				io.WriteString(w, `{"object":"list","data":[{"id":"item_sample","name":"sample"}],"has_more":false}`)
			}))
			t.Cleanup(server.Close)
			config := map[string]any{
				"home": t.TempDir(), "endpoint": server.URL, "mode": "complete", "action": "discover",
				"args": strings.Fields("openai " + tc.command), "height": tc.height, "expected": 0,
			}
			if strings.HasPrefix(tc.command, "widgets ") {
				config["process"] = "TestNewListCommandProcess"
			}
			if strings.Contains(tc.command, "--browse=false") || strings.Contains(tc.command, "--format json") {
				config["action"] = "print-return"
			}
			out := runResourceListObserver(t, config)
			if strings.Contains(tc.command, "--format json") {
				jsonOutput, _, _ := strings.Cut(out, "NEXT_COMMAND")
				require.JSONEq(t, `[{"id":"item_sample","name":"sample"}]`, jsonOutput)
			}
			require.EqualValues(t, 1, calls.Load())
			if tc.command == "widgets list-limit --pager" {
				require.EqualValues(t, 1, lookups.Load())
			} else {
				require.Zero(t, lookups.Load())
			}
		})
	}
}

// A static lookup and a primary SDK list can share one client.
func TestMainRegisteredListLookupKeepsPrimaryPagination(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)
	const broken = `{"object":"list","data":[],"has_more":true}`
	const first = `{"object":"list","data":[{"id":"container_a"}],"has_more":true}`
	const last = `{"object":"list","data":[{"id":"container_b"}],"has_more":false}`
	for _, tc := range []struct {
		name, verb, format string
		pages              []string
		cap                bool
		wantIDs            []string
		exit               int
	}{
		{"lookup-default", "list-limit", "", []string{broken}, false, nil, 1},
		{"lookup-jsonl", "list-limit", "jsonl", []string{broken}, false, nil, 1},
		{"lookup-json", "list-limit", "json", []string{broken}, false, nil, 1},
		{"single-list-control", "list-nullable", "jsonl", []string{broken}, false, nil, 1},
		{"complete", "list-limit", "jsonl", []string{first, last}, false, []string{"container_a", "container_b"}, 0},
		{"capped", "list-limit", "jsonl", []string{first}, true, []string{"container_a"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls, lookups atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/webhook_event_types" {
					lookups.Add(1)
					io.WriteString(w, `{"object":"list","data":["batch.completed"]}`)
					return
				}
				n := int(calls.Add(1))
				if r.URL.Path != "/v1/containers" || r.URL.Query().Has("limit") || n > len(tc.pages) {
					t.Errorf("unexpected primary request %d: %s", n, r.URL)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if n == 2 && r.URL.Query().Get("after") != "container_a" {
					t.Errorf("unexpected SDK continuation: %s", r.URL)
				}
				io.WriteString(w, tc.pages[n-1])
			}))
			t.Cleanup(server.Close)
			args := []string{"-test.run=^TestNewListCommandProcess$", "--", "openai", "widgets", tc.verb}
			if tc.format != "" {
				args = append(args, "--format", tc.format)
			}
			if tc.cap {
				args = append(args, "--max-items", "1")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, binary, args...)
			child.Env = []string{"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "CI=1", "HOME=" + t.TempDir(),
				"OPENAI_BASE_URL=" + server.URL + "/v1", "OPENAI_API_KEY=synthetic-widget-key", "NO_COLOR=1"}
			var stdout, stderr bytes.Buffer
			child.Stdout, child.Stderr = &stdout, &stderr
			err := child.Run()
			require.NotNil(t, child.ProcessState, "start child process: %v", err)
			require.Equal(t, tc.exit, child.ProcessState.ExitCode(), stderr.String())
			require.EqualValues(t, len(tc.pages), calls.Load())
			if tc.verb == "list-limit" {
				require.EqualValues(t, 1, lookups.Load())
			} else {
				require.Zero(t, lookups.Load())
			}
			if tc.exit != 0 {
				require.Contains(t, stderr.String(), "missing or repeated cursor")
				require.NotContains(t, stdout.String(), "batch.completed")
				return
			}
			require.NoError(t, err)
			require.Empty(t, stderr.String())
			decoder := json.NewDecoder(&stdout)
			for _, id := range tc.wantIDs {
				var row struct{ ID string }
				require.NoError(t, decoder.Decode(&row))
				require.Equal(t, id, row.ID)
			}
			require.ErrorIs(t, decoder.Decode(new(json.RawMessage)), io.EOF)
		})
	}
}

func TestMainSkillsPagerTotals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		flags  []string
		spans  []string
		limits []string
		offset int
	}{
		{"complete", nil, []string{"1–3", "4–6", "7–8/8", "4–6/8"}, []string{"3", "3", "3"}, 0},
		{"initial-cursor", []string{"--after", "skill_002"}, []string{"1–3", "4–6"}, []string{"3", "3"}, 2},
		{"item-cap", []string{"--max-items", "5"}, []string{"1–3", "4–5"}, []string{"3", "2"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, calls := listPageSizeFixture(t, "skill", 8, tc.limits...)
			runResourceListObserver(t, map[string]any{
				"args":     append([]string{"openai", "skills", "list", "--pager"}, tc.flags...),
				"endpoint": server.URL, "home": t.TempDir(), "width": 101, "height": 5, "mode": "normal",
				"action": "list-range", "rowPrefix": "skill_", "spans": tc.spans, "offset": tc.offset,
			})
			require.EqualValues(t, len(tc.limits), calls.Load())
		})
	}
}

func TestMainSkillsPagerResizeDuringRetry(t *testing.T) {
	pages, successful := listPageSizeFixture(t, "skill", 31, "8", "18")
	var attempts atomic.Int32
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_state":
			fmt.Fprintf(w, `{"requests":%d}`, attempts.Load())
		case "/_release":
			close(release)
		default:
			if attempts.Add(1) != 1 {
				pages.Config.Handler.ServeHTTP(w, r)
				return
			}
			if r.URL.Query().Get("limit") != "8" {
				t.Errorf("unexpected first request: %s", r.URL)
			}
			select {
			case <-release:
				w.Header().Set("retry-after-ms", "10")
				http.Error(w, "retry this page", http.StatusServiceUnavailable)
			case <-r.Context().Done():
			}
		}
	}))
	t.Cleanup(server.Close)
	runResourceListObserver(t, map[string]any{
		"args": []string{"openai", "skills", "list", "--pager"}, "endpoint": server.URL,
		"home": t.TempDir(), "width": 101, "height": 10, "mode": "normal",
		"action": "list-resize-retry", "rowPrefix": "skill_",
	})
	require.EqualValues(t, 3, attempts.Load())
	require.EqualValues(t, 2, successful.Load())
}
