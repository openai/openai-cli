package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestMainDebugCapturePaginationPreservesPages(t *testing.T) {
	page := func(first int, more bool) string {
		return fmt.Sprintf(`{"object":"list","data":[{"id":"file_synthetic_%d","object":"file","filename":"synthetic-private-page-%d-a.txt","purpose":"assistants","bytes":101},{"id":"file_synthetic_%d","object":"file","filename":"synthetic-private-page-%d-b.txt","purpose":"assistants","bytes":202}],"first_id":"file_synthetic_%d","last_id":"file_synthetic_%d","has_more":%t}`,
			first, first, first+1, first, first, first+1, more)
	}
	pages := []string{page(1, true), page(3, false)}
	for _, tc := range []struct {
		name, maxItems string
		flags          []string
		items, pages   int
	}{
		{"readable", "-1", nil, 4, 2},
		{"JSONL", "-1", []string{"--format", "jsonl"}, 4, 2},
		{"raw extraction", "-1", []string{"--format", "text", "--transform", "id", "--raw-output"}, 4, 2},
		{"page boundary", "2", []string{"--format", "json"}, 2, 1},
		{"partial second page", "3", []string{"--format", "json"}, 3, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var cursors []string
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()
				cursor := query.Get("after")
				mu.Lock()
				requests++
				unexpected := requests > 2*tc.pages
				if !unexpected {
					cursors = append(cursors, cursor)
				}
				mu.Unlock()
				if unexpected {
					t.Error("pagination made an unexpected extra request")
					http.Error(w, "unexpected synthetic request", http.StatusBadRequest)
					return
				}
				if r.Method != http.MethodGet || r.URL.Path != "/files" || query.Get("limit") != "2" || query.Get("order") != "asc" || query.Get("purpose") != "assistants" {
					t.Errorf("pagination changed request: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				switch cursor {
				case "":
					io.WriteString(w, pages[0])
				case "file_synthetic_2":
					io.WriteString(w, pages[1])
				default:
					http.Error(w, "unexpected synthetic cursor", http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			args := append(slices.Clone(tc.flags), "files", "list", "--limit", "2", "--order", "asc", "--purpose", "assistants", "--max-items", tc.maxItems)
			control := runDebugCaptureCommand(t, server, args...)
			flags := []string{"--debug", "--debug-body", "--debug-sse", "--debug-event-timing",
				"--debug-max-bytes", fmt.Sprint(max(len(pages[0]), len(pages[1]))), "--debug-max-events", "1"}
			got := runDebugCaptureCommand(t, server, append(flags, args...)...)
			wantDiagnostic := ""
			wantHints := 0
			if tc.name == "readable" {
				wantDiagnostic = resourceSummaryHint + "\n"
				wantHints = 1
			}
			if control.code != 0 || control.stderr != wantDiagnostic || got.code != control.code || got.stdout != control.stdout {
				t.Fatalf("capture changed paginated output: control=%+v capture=%+v", control, got)
			}
			if count := strings.Count(got.stderr, resourceSummaryHint+"\n"); count != wantHints {
				t.Errorf("capture changed the summary hint count: got %d, want %d", count, wantHints)
			}
			mu.Lock()
			actualCursors := slices.Clone(cursors)
			mu.Unlock()
			oneRun := []string{"", "file_synthetic_2"}[:tc.pages]
			wantCursors := append(slices.Clone(oneRun), oneRun...)
			if !slices.Equal(actualCursors, wantCursors) {
				t.Fatalf("capture changed cursor requests: got %q, want %q", actualCursors, wantCursors)
			}
			for number := 1; number <= 4; number++ {
				want := 0
				if number <= tc.items {
					want = 1
				}
				if count := strings.Count(got.stdout, fmt.Sprintf("file_synthetic_%d", number)); count != want {
					t.Errorf("file %d appears %d times, want %d: %q", number, count, want, got.stdout)
				}
			}
			if tc.name == "raw extraction" && got.stdout != "file_synthetic_1\nfile_synthetic_2\nfile_synthetic_3\nfile_synthetic_4\n" {
				t.Errorf("raw extraction bytes changed: %q", got.stdout)
			}
			if strings.Count(got.stderr, "response headers received after") != tc.pages || strings.Count(got.stderr, "response body (redacted;") != tc.pages {
				t.Fatalf("capture did not separate page attempts: %q", got.stderr)
			}
			for index := range tc.pages {
				attempt := index + 1
				assertDebugTimingStage(t, got.stderr, attempt, "response headers received", "200 OK")
				assertDebugTimingStage(t, got.stderr, attempt, "first response data read", "")
				assertDebugTimingStage(t, got.stderr, attempt, "response body fully consumed", "")
				prefix := fmt.Sprintf("HTTP attempt %d: response body (redacted; %d bytes observed; complete read): ", attempt, len(pages[index]))
				matches := regexp.MustCompile(regexp.QuoteMeta(prefix)+`([^\r\n]+)\n`).FindAllStringSubmatch(got.stderr, -1)
				if len(matches) != 1 || !strings.Contains(matches[0][1], fmt.Sprintf(`"has_more":%t`, index == 0)) {
					t.Fatalf("capture mixed page body state for attempt %d: %q", attempt, got.stderr)
				}
			}
			for _, unexpected := range []string{"capture truncated", "<omitted:", "SSE frame ", "SSE event ", fmt.Sprintf("HTTP attempt %d:", tc.pages+1)} {
				if strings.Contains(got.stderr, unexpected) {
					t.Errorf("pagination emitted unexpected diagnostics %q: %q", unexpected, got.stderr)
				}
			}
			assertDebugCapturePrivate(t, got.stderr, "synthetic-private-page", "file_synthetic_")
		})
	}
}
