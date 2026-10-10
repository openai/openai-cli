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
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func finiteJSONListRecords(t *testing.T, output string) []json.RawMessage {
	t.Helper()
	var records []json.RawMessage
	if err := json.Unmarshal([]byte(output), &records); err != nil {
		t.Fatalf("expected one complete JSON array: %v; stdout=%q", err, output)
	}
	if records == nil {
		t.Fatalf("expected an array, including [] for no records; stdout=%q", output)
	}
	return records
}

func finiteJSONValue(t *testing.T, raw string) any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestMainDispatchFiniteJSONListDocuments(t *testing.T) {
	for _, tc := range []struct {
		name, limit         string
		first, second, want []string
		calls               int
	}{
		{name: "empty", first: []string{}, want: []string{}, calls: 1},
		{name: "single", first: []string{"file_a"}, want: []string{"file_a"}, calls: 1},
		{name: "multiple pages", first: []string{"file_a", "file_b"}, second: []string{"file_b", "file_c"}, want: []string{"file_a", "file_b", "file_b", "file_c"}, calls: 2},
		{name: "zero", limit: "0", first: []string{"file_a"}, second: []string{"file_b"}, want: []string{}, calls: 1},
		{name: "one", limit: "1", first: []string{"file_a", "file_b"}, second: []string{"file_c"}, want: []string{"file_a"}, calls: 1},
		{name: "page boundary", limit: "2", first: []string{"file_a", "file_b"}, second: []string{"file_c"}, want: []string{"file_a", "file_b"}, calls: 1},
		{name: "unlimited", limit: "-1", first: []string{"file_a", "file_b"}, second: []string{"file_c"}, want: []string{"file_a", "file_b", "file_c"}, calls: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/files" || r.URL.Query().Get("limit") != "2" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Query().Get("after") {
				case "":
					io.WriteString(w, resourceFilePage(tc.first, len(tc.second) > 0))
				default:
					if len(tc.first) == 0 || r.URL.Query().Get("after") != tc.first[len(tc.first)-1] {
						t.Errorf("unexpected pagination cursor: %s", r.URL)
					}
					io.WriteString(w, resourceFilePage(tc.second, false))
				}
			}))
			defer server.Close()
			args := []string{"files", "list", "--format", "JsOn", "--limit", "2"}
			if tc.limit != "" {
				args = append(args, "--max-items", tc.limit)
			}
			got := runReadableCommand(t, server, args...)
			if got.code != 0 || got.stderr != "" || int(calls.Load()) != tc.calls {
				t.Fatalf("result=%+v requests=%d; want %d", got, calls.Load(), tc.calls)
			}
			records := finiteJSONListRecords(t, got.stdout)
			var ids []string
			for _, raw := range records {
				var record struct {
					ID string `json:"id"`
				}
				if err := json.Unmarshal(raw, &record); err != nil {
					t.Fatal(err)
				}
				ids = append(ids, record.ID)
			}
			if !slices.Equal(ids, tc.want) {
				t.Fatalf("ids=%q; want %q", ids, tc.want)
			}
		})
	}
}

func TestMainDispatchFiniteJSONListResourceFamilies(t *testing.T) {
	// The route metadata must select finite lists, including list-like methods.
	const item = `{"id":"synthetic_record","object":"synthetic","unknown":{"large":9007199254740993,"text":"資料-é","data":[1,2]}}`
	for _, tc := range []struct {
		path string
		args []string
	}{
		{"/models", []string{"models", "list"}},
		{"/files", []string{"files", "list"}},
		{"/batches", []string{"batches", "list"}},
		{"/vector_stores", []string{"vector-stores", "list"}},
		{"/vector_stores/vs_synthetic/files", []string{"vector-stores:files", "list", "vs_synthetic"}},
		{"/vector_stores/vs_synthetic/file_batches/batch_synthetic/files", []string{"vector-stores:file-batches", "list-files", "vs_synthetic", "batch_synthetic"}},
		{"/vector_stores/vs_synthetic/search", []string{"vector-stores", "search", "vs_synthetic", "--query", "synthetic"}},
		{"/vector_stores/vs_synthetic/files/file_synthetic/content", []string{"vector-stores:files", "content", "vs_synthetic", "file_synthetic"}},
		{"/fine_tuning/jobs", []string{"fine-tuning:jobs", "list"}},
		{"/fine_tuning/jobs/ftjob_synthetic/events", []string{"fine-tuning:jobs", "list-events", "ftjob_synthetic"}},
		{"/fine_tuning/jobs/ftjob_synthetic/checkpoints", []string{"fine-tuning:jobs:checkpoints", "list", "ftjob_synthetic"}},
		{"/fine_tuning/checkpoints/ft_synthetic/permissions", []string{"fine-tuning:checkpoints:permissions", "list", "ft_synthetic"}},
		{"/containers", []string{"containers", "list"}},
		{"/containers/cntr_synthetic/files", []string{"containers:files", "list", "cntr_synthetic"}},
		{"/videos", []string{"videos", "list"}},
		{"/skills", []string{"skills", "list"}},
		{"/skills/skill_synthetic/versions", []string{"skills:versions", "list", "skill_synthetic"}},
		{"/webhook_endpoints", []string{"webhooks", "list"}},
		{"/chat/completions", []string{"chat:completions", "list"}},
		{"/chat/completions/chatcmpl_synthetic/messages", []string{"chat:completions:messages", "list", "chatcmpl_synthetic"}},
		{"/responses/resp_synthetic/input_items", []string{"responses:input-items", "list", "resp_synthetic"}},
		{"/conversations/conv_synthetic/items", []string{"conversations:items", "list", "conv_synthetic"}},
		{"/assistants", []string{"beta:assistants", "list"}},
		{"/threads/thread_synthetic/messages", []string{"beta:threads:messages", "list", "thread_synthetic"}},
		{"/threads/thread_synthetic/runs", []string{"beta:threads:runs", "list", "thread_synthetic"}},
		{"/threads/thread_synthetic/runs/run_synthetic/steps", []string{"beta:threads:runs:steps", "list", "thread_synthetic", "run_synthetic"}},
		{"/responses/resp_synthetic/input_items", []string{"beta:responses:input-items", "list", "resp_synthetic"}},
		{"/chatkit/threads", []string{"beta:chatkit:threads", "list"}},
		{"/chatkit/threads/thread_synthetic/items", []string{"beta:chatkit:threads", "list-items", "thread_synthetic"}},
		{"/organization/projects", []string{"admin:organization:projects", "list"}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != tc.path {
					t.Errorf("path=%s; want %s", r.URL.Path, tc.path)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"object":"list","data":[%s],"has_more":false}`, item)
			}))
			defer server.Close()
			got := runMainDispatchWithEnv(t, "bash", []string{
				"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=sk-fake-list-test",
				"OPENAI_ADMIN_KEY=sk-fake-admin-list-test", "FORCE_COLOR=0",
			}, append([]string{"openai", "--format", "json"}, tc.args...)...)
			if got.code != 0 || got.stderr != "" || requests.Load() != 1 {
				t.Fatalf("result=%+v requests=%d", got, requests.Load())
			}
			records := finiteJSONListRecords(t, got.stdout)
			if len(records) != 1 || !reflect.DeepEqual(finiteJSONValue(t, string(records[0])), finiteJSONValue(t, item)) {
				t.Fatalf("finite list changed full API record: %q", got.stdout)
			}
		})
	}
}

func TestMainDispatchFiniteJSONListCursorVariants(t *testing.T) {
	for _, tc := range []struct {
		name, path, cursorField, cursor string
		args                            []string
	}{
		{"admin next cursor", "/organization/roles", "next", "opaque_next_page", []string{"admin:organization:roles", "list"}},
		{"conversation last ID", "/conversations/conv_synthetic/items", "last_id", "item_a", []string{"conversations:items", "list", "conv_synthetic"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var cursors []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				cursor := r.URL.Query().Get("after")
				mu.Lock()
				cursors = append(cursors, cursor)
				mu.Unlock()
				if r.Method != http.MethodGet || r.URL.Path != tc.path {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				switch cursor {
				case "":
					fmt.Fprintf(w, `{"data":[{"id":"item_a"}],"has_more":true,%q:%q}`, tc.cursorField, tc.cursor)
				case tc.cursor:
					io.WriteString(w, `{"data":[{"id":"item_b"}],"has_more":false}`)
				default:
					t.Errorf("unexpected cursor: %q", cursor)
					http.Error(w, "unexpected cursor", http.StatusBadRequest)
				}
			}))
			defer server.Close()
			got := runMainDispatchWithEnv(t, "bash", []string{
				"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=sk-fake-list-test",
				"OPENAI_ADMIN_KEY=sk-fake-admin-list-test", "FORCE_COLOR=0",
			}, append([]string{"openai", "--format", "json"}, tc.args...)...)
			mu.Lock()
			actualCursors := slices.Clone(cursors)
			mu.Unlock()
			if got.code != 0 || got.stderr != "" || !slices.Equal(actualCursors, []string{"", tc.cursor}) {
				t.Fatalf("result=%+v cursors=%q", got, actualCursors)
			}
			records := finiteJSONListRecords(t, got.stdout)
			if len(records) != 2 || !reflect.DeepEqual(finiteJSONValue(t, got.stdout), finiteJSONValue(t, `[{"id":"item_a"},{"id":"item_b"}]`)) {
				t.Fatalf("cursor pages did not form one ordered array: %q", got.stdout)
			}
		})
	}
}

func TestMainDispatchFiniteJSONListLargeRecord(t *testing.T) {
	// Generate the fixture in memory. Its size probes new buffering or line caps.
	payload := strings.Repeat("x", (2<<20)+1) + "資料\nlast"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"object": "list", "has_more": false,
			"data": []any{map[string]any{"id": "file_large", "object": "file", "synthetic_large_payload": payload}},
		}); err != nil {
			t.Errorf("write synthetic page: %v", err)
		}
	}))
	defer server.Close()
	got := runReadableCommand(t, server, "files", "list", "--format", "json")
	if got.code != 0 || got.stderr != "" {
		t.Fatalf("large record failed: code=%d stderr=%q", got.code, got.stderr)
	}
	records := finiteJSONListRecords(t, got.stdout)
	if len(records) != 1 {
		t.Fatalf("record count=%d", len(records))
	}
	var actual struct {
		Payload string `json:"synthetic_large_payload"`
	}
	if err := json.Unmarshal(records[0], &actual); err != nil {
		t.Fatal(err)
	}
	if actual.Payload != payload {
		t.Fatalf("large record changed: length=%d want=%d", len(actual.Payload), len(payload))
	}
}

func TestMainDispatchFiniteJSONListModelsSelection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"object":"list","data":[{"id":"model_c","object":"model"},{"id":"other_b","object":"model"},{"id":"model_a","object":"model"}]}`)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name       string
		args, want []string
	}{
		{"response order", nil, []string{"model_c", "other_b", "model_a"}},
		{"limit before default presentation", []string{"--max-items", "1"}, []string{"model_c"}},
		{"filter before limit", []string{"--filter", "id~^model_", "--max-items", "1"}, []string{"model_a"}},
		{"descending before limit", []string{"--filter", "id~^model_", "--sort-by", "~id", "--max-items", "1"}, []string{"model_c"}},
		{"empty selection", []string{"--filter", "id=missing"}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runReadableCommand(t, server, append([]string{"models", "list", "--format", "json"}, tc.args...)...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("result=%+v", got)
			}
			var ids []string
			for _, raw := range finiteJSONListRecords(t, got.stdout) {
				var record struct {
					ID string `json:"id"`
				}
				if err := json.Unmarshal(raw, &record); err != nil {
					t.Fatal(err)
				}
				ids = append(ids, record.ID)
			}
			if !slices.Equal(ids, tc.want) {
				t.Fatalf("ids=%q; want %q", ids, tc.want)
			}
		})
	}
}

func TestMainDispatchFiniteJSONListPreservesExplicitModes(t *testing.T) {
	const item = `{"id":"file_synthetic","object":"file","filename":"synthetic.txt","unknown":{"keep":true}}`
	const page = `{"object":"list","data":[` + item + `,` + item + `],"has_more":false}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, page)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name   string
		args   []string
		values []string
	}{
		{"jsonl", []string{"--format", "jsonl"}, []string{item, item}},
		{"raw envelope", []string{"--format", "raw"}, []string{page}},
		{"raw output", []string{"--format", "json", "--raw-output"}, []string{item, item}},
		{"explicit transform", []string{"--format", "json", "--transform", "filename"}, []string{`"synthetic.txt"`, `"synthetic.txt"`}},
		{"object transform", []string{"--format", "json", "--transform", "unknown"}, []string{`{"keep":true}`, `{"keep":true}`}},
		{"identity transform", []string{"--format", "json", "--transform", "@this"}, []string{item, item}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runReadableCommand(t, server, append(slices.Clone(tc.args), "files", "list")...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("result=%+v", got)
			}
			decoder := json.NewDecoder(strings.NewReader(got.stdout))
			decoder.UseNumber()
			for _, want := range tc.values {
				var actual any
				if err := decoder.Decode(&actual); err != nil || !reflect.DeepEqual(actual, finiteJSONValue(t, want)) {
					t.Fatalf("preserved mode changed: stdout=%q error=%v", got.stdout, err)
				}
			}
			var extra any
			if err := decoder.Decode(&extra); err != io.EOF {
				t.Fatalf("unexpected extra record: %q error=%v", got.stdout, err)
			}
		})
	}
	got := runReadableCommand(t, server, "--format", "json", "--transform", "filename", "--raw-output", "files", "list")
	if got.code != 0 || got.stderr != "" || got.stdout != "synthetic.txt\nsynthetic.txt\n" {
		t.Fatalf("raw extraction changed: %+v", got)
	}
}

func TestMainDispatchFiniteJSONListDoesNotInferDataField(t *testing.T) {
	const object = `{"id":"model_synthetic","object":"model","data":[{"id":"nested"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, object)
	}))
	defer server.Close()
	got := runReadableCommand(t, server, "models", "retrieve", "model_synthetic", "--format", "json")
	if got.code != 0 || got.stderr != "" {
		t.Fatalf("result=%+v", got)
	}
	var actual any
	if err := json.Unmarshal([]byte(got.stdout), &actual); err != nil || !reflect.DeepEqual(actual, finiteJSONValue(t, object)) {
		t.Fatalf("single object with data changed: %+v error=%v", got, err)
	}
}

func TestMainDispatchFiniteJSONListPreservesSingleResponseListEnvelope(t *testing.T) {
	const envelope = `{"object":"list","data":[{"type":"response.completed","description":"synthetic event"}],"unknown":{"preserved":true}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/webhook_event_types" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, envelope)
	}))
	defer server.Close()
	got := runReadableCommand(t, server, "webhooks:event-types", "list", "--format", "json")
	if got.code != 0 || got.stderr != "" {
		t.Fatalf("single-response list failed: %+v", got)
	}
	var actual map[string]any
	if err := json.Unmarshal([]byte(got.stdout), &actual); err != nil || !reflect.DeepEqual(actual, finiteJSONValue(t, envelope)) {
		t.Fatalf("single-response list envelope changed: %+v error=%v", got, err)
	}
}

func TestMainDispatchFiniteJSONListUpstreamFailures(t *testing.T) {
	for _, tc := range []struct {
		name, failure string
		status        int
		first         bool
	}{
		{"initial API failure", `{"error":{"message":"synthetic initial failure","type":"invalid_request_error"}}`, http.StatusBadRequest, true},
		{"later API failure", `{"error":{"message":"synthetic later failure","type":"invalid_request_error"}}`, http.StatusBadRequest, false},
		{"initial malformed page", `{"data":[`, http.StatusOK, true},
		{"later malformed page", `{"data":[`, http.StatusOK, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if !tc.first && r.URL.Query().Get("after") == "" {
					io.WriteString(w, resourceFilePage([]string{"file_a"}, true))
					return
				}
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.failure)
			}))
			defer server.Close()
			got := runReadableCommand(t, server, "--format", "json", "files", "list")
			if got.code == 0 || got.stderr == "" {
				t.Fatalf("failure reported success: %+v", got)
			}
			var diagnostic map[string]any
			if err := json.Unmarshal([]byte(got.stderr), &diagnostic); err != nil {
				t.Fatalf("invalid structured error: %+v error=%v", got, err)
			}
			if tc.first {
				if got.stdout != "" {
					t.Fatalf("initial failure wrote stdout: %+v", got)
				}
				return
			}
			assertFiniteJSONListPartial(t, got.stdout, "file_a")
		})
	}
}

func TestMainDispatchFiniteJSONListInvalidItemRecovery(t *testing.T) {
	const message = "The API returned an invalid JSON list item. Output may be incomplete. Check the response source before repeating the command."
	// The JSON syntax remains valid. Invalid UTF-8 must reach list-item validation,
	// rather than failing in the SDK's page decoder before presentation starts.
	const invalidItem = "{\"id\":\"file_invalid\",\"object\":\"file\",\"filename\":\"synthetic-private-file_\xff\",\"private_note\":\"sk-fake-private-list-marker https://synthetic.invalid/?token=fake\\u001b]52;c;synthetic-private-secret\\u0007\\u202e\"}"
	for _, partial := range []bool{false, true} {
		for _, tc := range []struct {
			name, format string
			flags        []string
			extracted    bool
		}{
			{name: "inherited JSON", format: "json"},
			{name: "text override", format: "text", flags: []string{"--format-error", "text"}},
			{name: "JSONL", format: "jsonl", flags: []string{"--format-error", "jsonl"}},
			{name: "YAML", format: "yaml", flags: []string{"--format-error", "yaml"}},
			{name: "raw error", format: "raw", flags: []string{"--format-error", "raw"}},
			{name: "extracted JSON", format: "json", flags: []string{"--transform-error", "message"}, extracted: true},
			{name: "extracted raw error", format: "raw", flags: []string{"--format-error", "raw", "--transform-error", "message"}, extracted: true},
			{name: "extracted text", format: "text", flags: []string{"--format-error", "text", "--transform-error", "message"}, extracted: true},
			{name: "quiet JSON", format: "json", flags: []string{"--quiet"}},
			{name: "quiet text", format: "text", flags: []string{"--quiet", "--format-error", "text"}},
		} {
			t.Run(fmt.Sprintf("partial=%t/%s", partial, tc.name), func(t *testing.T) {
				var requests atomic.Int32
				var corrected atomic.Bool
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Method != http.MethodGet || r.URL.Path != "/files" {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					}
					w.Header().Set("Content-Type", "application/json")
					if partial && r.URL.Query().Get("after") == "" {
						io.WriteString(w, resourceFilePage([]string{"file_a"}, true))
						return
					}
					if corrected.Load() {
						io.WriteString(w, resourceFilePage([]string{"file_recovered"}, false))
						return
					}
					fmt.Fprintf(w, `{"object":"list","data":[%s],"has_more":false}`, invalidItem)
				}))
				defer server.Close()
				args := append([]string{"--format", "json"}, tc.flags...)
				args = append(args, "files", "list")
				got := runReadableCommand(t, server, args...)
				wantRequests := int32(1)
				if partial {
					wantRequests = 2
				}
				if got.code != 1 || requests.Load() != wantRequests {
					t.Fatalf("invalid item status/retries changed: result=%+v requests=%d want=%d", got, requests.Load(), wantRequests)
				}
				if partial {
					assertFiniteJSONListPartial(t, got.stdout, "file_a")
				} else if got.stdout != "" {
					t.Fatalf("initial invalid item wrote stdout: %q", got.stdout)
				}
				for _, private := range []string{"file_invalid", "synthetic-private-", "sk-fake-private-", "synthetic.invalid", "token=fake", "\x1b", "\a", "\u202e", "\xff"} {
					if strings.Contains(got.stdout+got.stderr, private) {
						t.Fatalf("invalid item leaked fixture content %q: %+v", private, got)
					}
				}
				switch {
				case tc.format == "text":
					if got.stderr != message+"\n" {
						t.Fatalf("text recovery guidance=%q", got.stderr)
					}
				case tc.extracted:
					var actual string
					if err := json.Unmarshal([]byte(got.stderr), &actual); err != nil || actual != message {
						t.Fatalf("error extraction changed: stderr=%q error=%v", got.stderr, err)
					}
				default:
					actual := decodeMainStructuredError(t, tc.format, got.stderr)
					if !reflect.DeepEqual(actual, map[string]any{"message": message}) {
						t.Fatalf("structured recovery guidance=%#v", actual)
					}
				}

				// Correct the controlled response source, then repeat this read-only GET.
				// The CLI must not repair the fixture or repeat the failed request itself.
				corrected.Store(true)
				recovered := runReadableCommand(t, server, args...)
				if recovered.code != 0 || recovered.stderr != "" || requests.Load() != 2*wantRequests {
					t.Fatalf("corrected source did not recover: result=%+v requests=%d", recovered, requests.Load())
				}
				var ids []string
				for _, record := range finiteJSONListRecords(t, recovered.stdout) {
					var item struct {
						ID string `json:"id"`
					}
					if err := json.Unmarshal(record, &item); err != nil {
						t.Fatal(err)
					}
					ids = append(ids, item.ID)
				}
				wantIDs := []string{"file_recovered"}
				if partial {
					wantIDs = append([]string{"file_a"}, wantIDs...)
				}
				if !slices.Equal(ids, wantIDs) {
					t.Fatalf("corrected list IDs=%q want=%q", ids, wantIDs)
				}
			})
		}
	}
}

func assertFiniteJSONListPartial(t *testing.T, output, firstID string) {
	t.Helper()
	if json.Valid([]byte(output)) {
		t.Fatalf("failure completed the JSON document: %q", output)
	}
	decoder := json.NewDecoder(strings.NewReader(output))
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		t.Fatalf("missing array prefix: %q error=%v", output, err)
	}
	var first struct {
		ID string `json:"id"`
	}
	if err := decoder.Decode(&first); err != nil || first.ID != firstID {
		t.Fatalf("lost partial item: %q error=%v", output, err)
	}
	if token, err := decoder.Token(); err == nil {
		t.Fatalf("failure closed array with %v: %q", token, output)
	}
}

func startFiniteJSONListCommand(t *testing.T, server *httptest.Server) (*exec.Cmd, io.ReadCloser, *bytes.Buffer, context.Context) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	child := exec.CommandContext(ctx, binary, "-test.run=^TestMainDispatchProcess$", "--", "openai", "--format", "json", "files", "list")
	child.Env = []string{"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_API_KEY=sk-fake-list-test", "OPENAI_BASE_URL=" + server.URL, "FORCE_COLOR=0"}
	stderr := &bytes.Buffer{}
	child.Stderr = stderr
	stdout, err := child.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		stdout.Close()
		if child.ProcessState == nil {
			child.Wait()
		}
	})
	return child, stdout, stderr, ctx
}

func readFiniteJSONListFirst(t *testing.T, ctx context.Context, reader io.Reader) string {
	t.Helper()
	type readResult struct {
		output string
		err    error
	}
	ready := make(chan readResult, 1)
	go func() {
		var captured bytes.Buffer
		decoder := json.NewDecoder(io.TeeReader(reader, &captured))
		token, err := decoder.Token()
		if err == nil && token != json.Delim('[') {
			err = fmt.Errorf("expected array prefix, got %v", token)
		}
		var first map[string]any
		if err == nil {
			err = decoder.Decode(&first)
		}
		if err == nil && first["id"] != "file_a" {
			err = fmt.Errorf("unexpected first record: %v", first)
		}
		ready <- readResult{captured.String(), err}
	}()
	select {
	case got := <-ready:
		if got.err != nil {
			t.Fatalf("first record: %v; stdout=%q", got.err, got.output)
		}
		return got.output
	case <-ctx.Done():
		t.Fatal("first record did not arrive while the next page waited")
		return ""
	}
}

func TestMainDispatchFiniteJSONListWritesBeforeNextPageCompletes(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after") == "" {
			io.WriteString(w, resourceFilePage([]string{"file_a"}, true))
			return
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, resourceFilePage([]string{"file_b"}, false))
	}))
	t.Cleanup(server.Close)
	child, stdout, stderr, ctx := startFiniteJSONListCommand(t, server)
	prefix := readFiniteJSONListFirst(t, ctx, stdout)
	releaseOnce.Do(func() { close(release) })
	rest, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil || stderr.Len() != 0 {
		t.Fatalf("command failed: %v stderr=%q", err, stderr.String())
	}
	if records := finiteJSONListRecords(t, prefix+string(rest)); len(records) != 2 {
		t.Fatalf("record count=%d", len(records))
	}
}

func TestMainDispatchFiniteJSONListPreservesEventStream(t *testing.T) {
	events := []string{
		`{"type":"response.output_text.delta","delta":"synthetic","output_index":0,"content_index":0,"sequence_number":0,"data":[1]}`,
		`{"type":"response.completed","response":{"id":"resp_synthetic","status":"completed","output":[]},"sequence_number":1}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range events {
			fmt.Fprintf(w, "data: %s\n\n", event)
		}
	}))
	defer server.Close()
	got := runReadableCommand(t, server, "responses", "create", "--format", "json", "--model", "fake-model", "--input", "synthetic", "--stream=true")
	if got.code != 0 || got.stderr != "" {
		t.Fatalf("result=%+v", got)
	}
	decoder := json.NewDecoder(strings.NewReader(got.stdout))
	decoder.UseNumber()
	for _, event := range events {
		var actual any
		if err := decoder.Decode(&actual); err != nil || !reflect.DeepEqual(actual, finiteJSONValue(t, event)) {
			t.Fatalf("event stream changed: %+v error=%v", got, err)
		}
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("extra event output: %+v error=%v", got, err)
	}
}
