// list-navigation-demo-api serves two fixed synthetic file pages.
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

type fixture struct {
	mu      sync.Mutex
	log     *os.File
	counts  map[string]int
	failure error
}

func (f *fixture) recordFailure(err error) {
	if err != nil {
		f.mu.Lock()
		f.failure = errors.Join(f.failure, err)
		f.mu.Unlock()
	}
}

func (f *fixture) writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	f.recordFailure(json.NewEncoder(w).Encode(value))
}

func (f *fixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if r.Method == http.MethodGet && len(parts) == 2 && parts[0] == "_counts" && validScene(parts[1]) && r.URL.RawQuery == "" {
		f.mu.Lock()
		count := f.counts[parts[1]]
		f.mu.Unlock()
		f.writeJSON(w, map[string]int{"requests": count})
		return
	}
	query := r.URL.Query()
	cursor := query.Get("after")
	order := query.Get("order")
	if r.Method != http.MethodGet || len(parts) != 3 || !validScene(parts[0]) ||
		parts[1] != "v1" || parts[2] != "files" || r.Header.Get("Authorization") != "Bearer synthetic-demo-key" ||
		query.Get("limit") != "2" || (order != "" && order != "desc") ||
		(cursor != "" && cursor != "file_002") || len(query) != 1+btoi(cursor != "")+btoi(order != "") {
		w.WriteHeader(http.StatusBadRequest)
		f.writeJSON(w, map[string]any{"error": map[string]string{"message": "Unexpected synthetic fixture request.", "type": "invalid_request_error"}})
		f.recordFailure(errors.New("fixture rejected an unexpected request"))
		return
	}
	page := 1
	if cursor != "" {
		page = 2
	}
	// Record only validated synthetic fields. Never record headers or arbitrary URLs.
	f.mu.Lock()
	err := json.NewEncoder(f.log).Encode(map[string]any{
		"scene": parts[0], "page": page, "cursor": cursor, "limit": 2, "received_unix_nano": time.Now().UnixNano(),
	})
	if err == nil {
		err = f.log.Sync()
	}
	if err == nil {
		f.counts[parts[0]]++
	} else {
		f.failure = errors.Join(f.failure, err)
	}
	f.mu.Unlock()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		f.writeJSON(w, map[string]string{"error": "Could not record the synthetic request."})
		return
	}
	items := []map[string]any{}
	for number := 2*page - 1; number <= 2*page; number++ {
		items = append(items, map[string]any{
			"id": fmt.Sprintf("file_%03d", number), "object": "file", "created_at": 1704067200,
			"filename": fmt.Sprintf("synthetic-%03d.jsonl", number), "purpose": "batch", "bytes": number * 1024,
		})
	}
	f.writeJSON(w, map[string]any{"object": "list", "data": items, "has_more": page == 1,
		"first_id": items[0]["id"], "last_id": items[1]["id"]})
}

func validScene(scene string) bool { return scene == "before" || scene == "after" }

func btoi(value bool) int {
	if value {
		return 1
	}
	return 0
}

func run() (result error) {
	if len(os.Args) != 3 {
		return errors.New("usage: list-navigation-demo-api ADDRESS_FILE REQUEST_LOG")
	}
	requests, err := os.OpenFile(os.Args[2], os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, requests.Close()) }()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	f := &fixture{log: requests, counts: make(map[string]int)}
	server := &http.Server{Handler: f, ReadHeaderTimeout: 5 * time.Second}
	if err := os.WriteFile(os.Args[1], []byte("http://"+listener.Addr().String()), 0600); err != nil {
		return errors.Join(err, listener.Close())
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		shutdownDone <- server.Shutdown(shutdown)
	}()
	err = server.Serve(listener)
	cancel()
	shutdownErr := <-shutdownDone
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return errors.Join(err, shutdownErr, f.failure)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
