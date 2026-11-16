package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Keep one server's state across the complete public workflow. IDs must come
// from earlier commands, and result order must not imply input order.
func TestMainBatchesDashboardParity(t *testing.T) {
	for _, terminal := range []string{"completed", "cancelled"} {
		t.Run(terminal, func(t *testing.T) {
			var input strings.Builder
			for i := 1; i <= 3; i++ {
				fmt.Fprintf(&input, "{\"custom_id\":\"request-%d\",\"method\":\"POST\",\"url\":\"/v1/responses\",\"body\":{\"model\":\"gpt-4.1-mini\",\"input\":\"Return blue.\"}}\n", i)
			}
			const fileID, batchID = "file_uploaded_returned", "batch_created_returned"
			const outputID, errorID = "file_output_returned", "file_error_returned"
			files := map[string]string{
				outputID: "{\"custom_id\":\"request-3\",\"response\":{\"status_code\":200}}\n{\"custom_id\":\"request-1\",\"response\":{\"status_code\":200}}\n",
				errorID:  "{\"custom_id\":\"request-2\",\"error\":{\"code\":\"synthetic_failure\"}}\n",
			}
			wantCreate := map[string]any{
				"input_file_id": fileID, "endpoint": "/v1/responses", "completion_window": "24h",
				"metadata":             map[string]any{"job": "synthetic parity"},
				"output_expires_after": map[string]any{"anchor": "created_at", "seconds": float64(86400)},
			}
			var mu sync.Mutex
			var requests []string
			var created map[string]any
			states, index := []string{"validating", "in_progress", "finalizing", "completed"}, 0
			batch := func() map[string]any {
				value := map[string]any{
					"id": batchID, "object": "batch", "status": states[index], "created_at": 1760000000,
					"expires_at": 1760086400, "request_counts": map[string]int{"total": 3, "completed": 2, "failed": 1},
					"errors":         map[string]any{"object": "list", "data": []any{}},
					"output_file_id": outputID, "error_file_id": errorID,
				}
				for key, item := range created {
					value[key] = item
				}
				return value
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				requests = append(requests, r.Method+" "+r.URL.RequestURI())
				if r.Header.Get("Authorization") != "Bearer sk-fake-readable-test" || r.Header.Get("OpenAI-Project") != "proj_synthetic" {
					t.Error("workflow lost request authentication or project context")
				}
				w.Header().Set("Content-Type", "application/json")
				var response any
				switch r.Method + " " + r.URL.Path {
				case "POST /files":
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Error(err)
						http.Error(w, "invalid synthetic upload", http.StatusBadRequest)
						return
					}
					defer r.MultipartForm.RemoveAll()
					file, _, err := r.FormFile("file")
					if err != nil {
						t.Error(err)
						http.Error(w, "missing synthetic file", http.StatusBadRequest)
						return
					}
					contents, readErr := io.ReadAll(file)
					closeErr := file.Close()
					if readErr != nil || closeErr != nil || string(contents) != input.String() || r.FormValue("purpose") != "batch" {
						t.Errorf("upload changed: read=%v close=%v purpose=%q", readErr, closeErr, r.FormValue("purpose"))
					}
					files[fileID] = string(contents)
					response = map[string]any{"id": fileID, "object": "file", "purpose": "batch", "bytes": len(contents)}
				case "POST /batches":
					if err := json.NewDecoder(r.Body).Decode(&created); err != nil || !reflect.DeepEqual(created, wantCreate) || files[fileID] == "" {
						t.Errorf("batch did not use uploaded input and exact fields: %v (%v)", created, err)
					}
					response = batch()
				case "GET /batches":
					response = map[string]any{"object": "list", "data": []any{batch()}, "has_more": false}
				case "GET /batches/" + batchID:
					response = batch()
					if index < len(states)-1 {
						index++
					}
				case "POST /batches/" + batchID + "/cancel":
					states, index = []string{"cancelling", "cancelled"}, 0
					response = batch()
				default:
					id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/files/"), "/content")
					if content, ok := files[id]; ok && r.Method == http.MethodGet && r.URL.Path == "/files/"+id+"/content" {
						io.WriteString(w, content)
						return
					}
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.Error(w, "unexpected synthetic request", http.StatusBadRequest)
					return
				}
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			run := func(code int, wantRequests []string, args ...string) string {
				t.Helper()
				mu.Lock()
				requests = nil
				mu.Unlock()
				got := runReadableCommand(t, server, append([]string{"--format", "json", "--project", "proj_synthetic"}, args...)...)
				mu.Lock()
				actualRequests := append([]string(nil), requests...)
				mu.Unlock()
				if got.code != code || !reflect.DeepEqual(actualRequests, wantRequests) {
					t.Fatalf("command %v: result=%+v requests=%v; want exit %d requests=%v", args, got, actualRequests, code, wantRequests)
				}
				if code == 0 && got.stderr != "" || code != 0 && !json.Valid([]byte(got.stderr)) || !json.Valid([]byte(got.stdout)) {
					t.Fatalf("command %v corrupted its output channels: %+v", args, got)
				}
				return got.stdout
			}
			object := func(text string) map[string]any {
				t.Helper()
				var value map[string]any
				if err := json.Unmarshal([]byte(text), &value); err != nil {
					t.Fatal(err)
				}
				return value
			}
			inputPath := filepath.Join(t.TempDir(), "requests with spaces.jsonl")
			if err := os.WriteFile(inputPath, []byte(input.String()), 0600); err != nil {
				t.Fatal(err)
			}
			uploaded := object(run(0, []string{"POST /files"}, "files", "upload", "--file", inputPath, "--purpose", "batch"))
			result := object(run(0, []string{"POST /batches"}, "batches", "create", "--input-file-id", uploaded["id"].(string),
				"--endpoint", "/v1/responses", "--completion-window", "24h", "--metadata", `{"job":"synthetic parity"}`,
				"--output-expires-after.anchor", "created_at", "--output-expires-after.seconds", "86400"))
			id := result["id"].(string)
			get := "GET /batches/" + id
			listed := run(0, []string{"GET /batches?limit=20"}, "batches", "list", "--limit", "20", "--max-items", "-1")
			if !strings.Contains(listed, id) {
				t.Fatal("list omitted the created batch")
			}
			inspected := object(run(0, []string{get}, "batches", "retrieve", id))
			for _, key := range []string{"created_at", "expires_at", "request_counts", "errors", "metadata"} {
				if inspected[key] == nil {
					t.Fatalf("retrieve omitted %s", key)
				}
			}
			waitRequests := []string{get, get, get}
			if terminal == "cancelled" {
				cancelled := object(run(0, []string{"POST /batches/" + id + "/cancel"}, "batches", "cancel", id))
				if cancelled["status"] != "cancelling" {
					t.Fatalf("cancel status=%v", cancelled["status"])
				}
				waitRequests = []string{get, get}
			}
			final := object(run(1, waitRequests, "batches", "retrieve", id, "--wait", "--poll-interval", "1ms"))
			if final["status"] != terminal || !reflect.DeepEqual(final["request_counts"], map[string]any{"total": float64(3), "completed": float64(2), "failed": float64(1)}) {
				t.Fatalf("wait lost terminal status or partial failures: %v", final)
			}
			recovered := map[string]map[string]bool{}
			for _, selected := range []string{"input", "output", "error"} {
				selectedID := final[selected+"_file_id"].(string)
				path := filepath.Join(t.TempDir(), selected+" recovered.jsonl")
				receipt := object(run(0, []string{get, "GET /files/" + selectedID + "/content"}, "batches", "download", id, "--file", selected, "--output", path))
				content, err := os.ReadFile(path)
				if err != nil || string(content) != files[selectedID] || receipt["file_id"] != selectedID || receipt["bytes"] != float64(len(content)) {
					t.Fatalf("%s download changed bytes or receipt: %v (%v)", selected, receipt, err)
				}
				recovered[selected] = map[string]bool{}
				for _, line := range bytes.Split(bytes.TrimSpace(content), []byte("\n")) {
					customID := object(string(line))["custom_id"].(string)
					if recovered[selected][customID] {
						t.Fatalf("duplicate %s custom_id: %s", selected, customID)
					}
					recovered[selected][customID] = true
				}
			}
			if len(recovered["input"]) != 3 || len(recovered["output"]) != 2 || len(recovered["error"]) != 1 {
				t.Fatalf("unexpected result counts: %v", recovered)
			}
			for customID := range recovered["input"] {
				if recovered["output"][customID] == recovered["error"][customID] {
					t.Fatalf("custom_id must appear in exactly one result file: %s", customID)
				}
			}
		})
	}
}
