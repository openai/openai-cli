// list-table-demo-api serves fixed synthetic list pages on loopback only.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: list-table-demo-api ADDRESS_FILE REQUEST_LOG")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(addressPath, requestsPath string) (result error) {
	requests, err := os.OpenFile(requestsPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, requests.Close()) }()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	var mu sync.Mutex
	var handlerErr error
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		scenario, resource, ok := route(r)
		page := 1
		if r.URL.Query().Get("after") != "" {
			page = 2
		}
		cursorValid := true
		if ok && page == 2 && (scenario == "multipage" || scenario == "partial" || scenario == "long-id") {
			first := listPage(scenario, resource, 1)
			cursorValid = r.URL.Query().Get("after") == first["last_id"]
		}
		status := http.StatusOK
		var payload any
		switch {
		case !ok || !cursorValid:
			scenario, resource, status = "rejected", "rejected", http.StatusNotFound
		case scenario == "error" || scenario == "partial" && page == 2:
			status = http.StatusBadRequest
		default:
			payload = listPage(scenario, resource, page)
		}
		if status != http.StatusOK {
			payload = map[string]any{"error": map[string]any{
				"message": "Synthetic list fixture failure.", "type": "invalid_request_error", "code": "synthetic_error",
			}}
		}
		// Only allowlisted route names and fixed integers enter the log.
		if err := json.NewEncoder(requests).Encode(map[string]any{
			"scenario": scenario, "resource": resource, "page": page, "status": status, "cursor_valid": cursorValid,
		}); err != nil {
			handlerErr = errors.Join(handlerErr, fmt.Errorf("write request log: %w", err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			handlerErr = errors.Join(handlerErr, fmt.Errorf("write fixture response: %w", err))
		}
	})
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		shutdownDone <- server.Shutdown(shutdown)
	}()
	if err := os.WriteFile(addressPath, []byte("http://"+listener.Addr().String()), 0600); err != nil {
		cancel()
		return errors.Join(err, <-shutdownDone)
	}
	err = server.Serve(listener)
	cancel()
	shutdownErr := <-shutdownDone
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	mu.Lock()
	defer mu.Unlock()
	return errors.Join(err, shutdownErr, handlerErr)
}

func route(r *http.Request) (scenario, resource string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if r.Method != http.MethodGet || len(parts) < 3 || parts[1] != "v1" {
		return "", "", false
	}
	scenario, resource = parts[0], strings.Join(parts[2:], "/")
	switch scenario {
	case "normal", "empty", "controls", "unknown", "long-id", "error", "multipage", "partial":
	default:
		return "", "", false
	}
	switch resource {
	case "files", "batches", "organization/projects":
		return scenario, resource, true
	}
	return "", "", false
}

func listPage(scenario, resource string, page int) map[string]any {
	items := []map[string]any{}
	if scenario != "empty" {
		switch resource {
		case "files":
			items = []map[string]any{
				{"id": "file-demo-support", "object": "file", "filename": "support-evals.jsonl", "purpose": "batch", "bytes": 18432, "status": "processed", "created_at": 1704067200},
				{"id": "file-demo-research-20261007", "object": "file", "filename": "評測-東京-🚀-résumé-and-a-long-dataset-name.jsonl", "purpose": "fine-tune", "bytes": 2097152, "status": "uploaded", "created_at": 1704067200},
			}
		case "batches":
			items = []map[string]any{
				{"id": "batch-demo-support", "object": "batch", "status": "completed", "endpoint": "/v1/responses", "input_file_id": "file-demo-support", "completion_window": "24h", "created_at": 1704067200, "request_counts": map[string]any{"total": 20, "completed": 20, "failed": 0}},
				{"id": "batch-demo-research-20261007", "object": "batch", "status": "in_progress", "endpoint": "/v1/responses", "input_file_id": "file-demo-research-20261007", "completion_window": "24h", "created_at": 1704067200, "request_counts": map[string]any{"total": 50, "completed": 10, "failed": 0}},
			}
		case "organization/projects":
			items = []map[string]any{
				{"id": "proj_demo_support", "object": "organization.project", "name": "Support evaluation", "status": "active", "created_at": 1704067200},
				{"id": "proj_demo_research_20261007", "object": "organization.project", "name": "Research 東京 🚀 with a deliberately long name", "status": "archived", "created_at": 1704067200, "archived_at": 1704153600},
			}
		}
		if scenario == "controls" {
			field := map[string]string{"files": "filename", "batches": "status", "organization/projects": "name"}[resource]
			items[1][field] = "demo-\x1b[31mred\x1b[0m\t\n\u202e"
		}
		if scenario == "unknown" {
			items[1]["synthetic_notice"] = "detail-002"
		}
		if scenario == "long-id" {
			prefix := map[string]string{"files": "file-", "batches": "batch-", "organization/projects": "proj-"}[resource]
			field := map[string]string{"files": "filename", "batches": "endpoint", "organization/projects": "name"}[resource]
			for index, item := range items {
				item["id"] = prefix + strings.Repeat("x", 77-len(prefix)-3) + fmt.Sprintf("-%02d", index+1)
				item[field] = fmt.Sprintf("p%03d", index+1)
				if resource == "batches" {
					item[field] = "/" + item[field].(string)
				}
			}
		}
		if scenario == "multipage" || scenario == "partial" || scenario == "long-id" {
			items = items[page-1 : page]
		}
	}
	more := (scenario == "multipage" || scenario == "partial" || scenario == "long-id") && page == 1
	result := map[string]any{"object": "list", "data": items, "has_more": more}
	if len(items) > 0 {
		result["first_id"], result["last_id"] = items[0]["id"], items[len(items)-1]["id"]
	}
	return result
}
