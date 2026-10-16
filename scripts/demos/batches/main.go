// batches-demo-api serves synthetic lifecycle and file data on loopback only.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) != 3 {
		panic("usage: batches-demo-api ADDRESS_FILE REQUEST_LOG")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	log, err := os.OpenFile(os.Args[2], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := log.Close(); err != nil {
			panic(err)
		}
	}()
	var mu sync.Mutex
	polls := map[string]int{}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-demo-key" {
			http.Error(w, "synthetic credentials required", 401)
			return
		}
		if r.Method != "GET" || (r.URL.Path != "/v1/batches/batch_before" && r.URL.Path != "/v1/batches/batch_after" && r.URL.Path != "/v1/files/file_output/content") {
			http.Error(w, "synthetic route only", 404)
			return
		}
		mu.Lock()
		polls[r.URL.Path]++
		n := polls[r.URL.Path]
		err := json.NewEncoder(log).Encode(map[string]any{"method": r.Method, "path": r.URL.Path})
		mu.Unlock()
		if err != nil {
			http.Error(w, "log failure", 500)
			return
		}
		if r.URL.Path == "/v1/files/file_output/content" {
			w.Header().Set("Content-Type", "application/jsonl")
			for i := 1; i <= 1000; i++ {
				if _, err := fmt.Fprintf(w, "{\"custom_id\":\"example-%d\",\"response\":{\"status_code\":200,\"body\":{\"output_text\":\"blue\"}}}\n", i); err != nil {
					fmt.Fprintln(os.Stderr, err)
					return
				}
			}
			return
		}
		status, finished := "in_progress", 420
		if n >= 2 {
			status, finished = "completed", 1000
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"id": r.URL.Path[len("/v1/batches/"):], "object": "batch", "status": status,
			"request_counts": map[string]int{"total": 1000, "completed": finished, "failed": 0}, "output_file_id": "file_output",
		}); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		done <- server.Shutdown(c)
	}()
	if err := os.WriteFile(os.Args[1], []byte("http://"+listener.Addr().String()+"/v1"), 0600); err != nil {
		panic(err)
	}
	err = server.Serve(listener)
	stop()
	closeErr := <-done
	if err != nil && err != http.ErrServerClosed {
		panic(err)
	}
	if closeErr != nil {
		panic(closeErr)
	}
}
