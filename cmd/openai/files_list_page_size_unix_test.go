//go:build !windows

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func filePageSizeFixture(t *testing.T, wantLimits ...string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	return listPageSizeFixture(t, "file", 55, wantLimits...)
}

// The service returns its remaining records in one response when limit is omitted.
func listPageSizeFixture(t *testing.T, kind string, total int, wantLimits ...string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	var cycle atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_state" {
			fmt.Fprintf(w, `{"requests":%d}`, calls.Load())
			return
		}
		call := int(calls.Add(1))
		wantLimit := wantLimits[min(call, len(wantLimits))-1]
		q := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != "/v1/"+kind+"s" || q.Get("limit") != wantLimit {
			t.Errorf("unexpected list request: %s %s (want limit %q)", r.Method, r.URL, wantLimit)
		}
		limit := total
		if q.Has("limit") {
			limit, _ = strconv.Atoi(q.Get("limit"))
		}
		start := 0
		// The cycle fixture returns this starting cursor again on page two, after resize.
		stalled := q.Get("after") == "file_stalled"
		if call == 1 && stalled {
			cycle.Store(true)
		}
		if stalled && call > 1 {
			t.Error("dispatched the same cursor after a terminal resize")
			http.Error(w, "repeated cursor reached fixture", http.StatusBadRequest)
			return
		}
		if q.Has("after") && !stalled {
			n, err := fmt.Sscanf(q.Get("after"), kind+"_%03d", &start)
			if n != 1 || err != nil || start < 1 || start > total {
				http.Error(w, "invalid fixture cursor", http.StatusBadRequest)
				return
			}
		}
		end := min(start+limit, total)
		if cycle.Load() && call == 2 {
			end = start + 2
		}
		var rows []string
		for i := start + 1; i <= end; i++ {
			id := fmt.Sprintf("%s_%03d", kind, i)
			if cycle.Load() && call == 2 && i == end {
				id = "file_stalled"
			}
			if kind == "container" {
				rows = append(rows, fmt.Sprintf(`{"id":%q,"name":"sample"}`, id))
			} else if kind == "skill" {
				rows = append(rows, fmt.Sprintf(`{"id":%q,"object":"skill","name":"sample","default_version":"1","latest_version":"1"}`, id))
			} else {
				rows = append(rows, fmt.Sprintf(`{"id":%q,"object":"file","filename":"sample.jsonl","bytes":16,"purpose":"batch","status":"processed"}`, id))
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"object":"list","data":[%s],"has_more":%t}`, strings.Join(rows, ","), end < total)
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

func TestMainFilesPagerPageSizeAndVisibleRange(t *testing.T) {
	for _, tc := range []struct {
		name, limit string
		flags       []string
		spans       []string
		offset      int
	}{
		{"preview", "25", nil, []string{"Files 1–25", "Files 26–50", "Files 1–25"}, 0},
		{"uncapped", "25", []string{"--pager"}, []string{"1–25", "26–50", "51–55/55", "26–50/55"}, 0},
		{"smaller-total-cap", "4", []string{"--pager", "--max-items", "4"}, []string{"1–4"}, 0},
		{"explicit-page-size", "3", []string{"--pager", "--limit", "3", "--max-items", "5"}, []string{"1–3", "4–5"}, 0},
		{"explicit-page-size-above-cap", "30", []string{"--pager", "--limit", "30", "--max-items", "5"}, []string{"1–5"}, 0},
		{"after", "25", []string{"--pager", "--after", "file_052"}, []string{"1–3"}, 52},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := filePageSizeFixture(t, tc.limit)
			runResourceListObserver(t, map[string]any{
				"args":     append([]string{"openai", "files", "list"}, tc.flags...),
				"endpoint": server.URL, "home": t.TempDir(), "width": 101, "height": 27,
				"mode": "normal", "action": "files-range", "spans": tc.spans, "offset": tc.offset,
			})
		})
	}
}

func TestMainFilesInlinePreviewReturnsWhenPageFits(t *testing.T) {
	for _, flags := range [][]string{nil, {"--browse"}} {
		server, calls := listPageSizeFixture(t, "file", 12, "12")
		screen := runResourceListObserver(t, map[string]any{
			"args": append([]string{"openai", "files", "list"}, flags...), "endpoint": server.URL,
			"home": t.TempDir(), "width": 101, "height": 14, "mode": "complete", "action": "print-return",
		})
		require.EqualValues(t, 1, calls.Load())
		require.Equal(t, 1, strings.Count(screen, "FILENAME"))
		for n := 1; n <= 12; n++ {
			require.Equal(t, 1, strings.Count(screen, fmt.Sprintf("file_%03d", n)))
		}
	}
}

func TestMainFilesInlinePreviewPreservesShellHistory(t *testing.T) {
	server, calls := filePageSizeFixture(t, "10")
	runResourceListObserver(t, map[string]any{
		"args": []string{"openai", "files", "list"}, "endpoint": server.URL,
		"home": t.TempDir(), "width": 101, "height": 12, "mode": "normal", "action": "preview-scrollback",
	})
	require.EqualValues(t, 1, calls.Load())
}

func TestMainFilesPagerPageSizeAfterResize(t *testing.T) {
	server, calls := filePageSizeFixture(t, "8", "8", "18", "7")
	runResourceListObserver(t, map[string]any{
		"args": []string{"openai", "files", "list", "--pager"}, "endpoint": server.URL,
		"home": t.TempDir(), "width": 101, "height": 10, "mode": "normal", "action": "files-resize",
	})
	require.EqualValues(t, 4, calls.Load())
}

func TestMainFilesPagerPageSizeUsesRemainingItemCap(t *testing.T) {
	server, calls := filePageSizeFixture(t, "8", "7")
	runResourceListObserver(t, map[string]any{
		"args":     []string{"openai", "files", "list", "--pager", "--max-items", "15", "--after", "file_003"},
		"endpoint": server.URL, "home": t.TempDir(), "width": 101, "height": 10,
		"mode": "normal", "action": "files-range", "spans": []string{"1–8", "9–15"}, "offset": 3,
	})
	require.EqualValues(t, 2, calls.Load())
}

func TestMainFilesPagerResizeStopsStalledCursor(t *testing.T) {
	server, calls := filePageSizeFixture(t, "8", "18")
	runResourceListObserver(t, map[string]any{
		"args":     []string{"openai", "files", "list", "--pager", "--after", "file_stalled", "--format-error", "json"},
		"endpoint": server.URL, "home": t.TempDir(), "width": 101, "height": 10,
		"mode": "normal", "action": "files-stalled-resize",
	})
	require.EqualValues(t, 2, calls.Load())
}

func TestMainFilesPagerPageSizeBounds(t *testing.T) {
	for _, tc := range []struct {
		name, limit string
		height      int
	}{
		{"one-row-terminal", "1", 1},
		{"forty-table-rows", "40", 42},
		{"server-maximum", "10000", 10003},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, calls := filePageSizeFixture(t, tc.limit)
			runResourceListObserver(t, map[string]any{
				"args":     []string{"openai", "files", "list", "--pager", "--after", "file_054"},
				"endpoint": server.URL, "home": t.TempDir(), "width": 12, "height": tc.height,
				"mode": "normal", "action": "files-first-request",
			})
			require.EqualValues(t, 1, calls.Load())
		})
	}
}

func TestMainFilesPageSizeOutsideBrowser(t *testing.T) {
	for _, mode := range []string{"print", "pipe", "json"} {
		t.Run(mode, func(t *testing.T) {
			server, calls := filePageSizeFixture(t, "")
			args := []string{"openai", "files", "list"}
			if mode == "pipe" {
				got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=" + server.URL + "/v1", "OPENAI_API_KEY=synthetic"}, args...)
				require.Equal(t, 0, got.code, got.stderr)
				require.Contains(t, got.stdout, "ID: file_055")
			} else {
				if mode == "print" {
					args = append(args, "--browse=false")
				} else {
					args = append(args, "--format", "json")
				}
				out := runResourceListObserver(t, map[string]any{
					"args": args, "endpoint": server.URL, "home": t.TempDir(), "mode": "complete",
					"action": "print-return", "expected": 0,
				})
				require.Contains(t, out, "file_055")
				if mode == "json" {
					doc, _, _ := strings.Cut(out, "NEXT_COMMAND")
					var items []map[string]any
					require.NoError(t, json.Unmarshal([]byte(doc), &items))
					require.Len(t, items, 55)
				}
			}
			require.EqualValues(t, 1, calls.Load())
		})
	}
}
