// global-flags-demo-api reflects a synthetic request's project header in model data.
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
		fmt.Fprintln(os.Stderr, "usage: global-flags-demo-api ADDRESS_FILE REQUEST_LOG")
		os.Exit(2)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	requests, err := os.OpenFile(os.Args[2], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := requests.Close(); err != nil {
			panic(fmt.Errorf("close demo request log: %w", err))
		}
	}()
	var logMu sync.Mutex
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		project := r.Header.Get("OpenAI-Project")
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" ||
			r.Header.Get("Authorization") != "Bearer synthetic-demo-key" ||
			(project != "" && project != "proj-example") {
			http.Error(w, "only the synthetic demo request is accepted", http.StatusNotFound)
			return
		}
		// Record only validated synthetic fields, never credentials or arbitrary headers.
		logMu.Lock()
		err := json.NewEncoder(requests).Encode(map[string]string{
			"method": r.Method, "path": r.URL.Path, "project": project,
		})
		logMu.Unlock()
		if err != nil {
			http.Error(w, "could not record synthetic request", http.StatusInternalServerError)
			return
		}
		if project == "" {
			project = "(missing)"
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []any{map[string]any{
				"id": "OpenAI-Project: " + project, "object": "model",
				"created": 0, "owned_by": "synthetic",
			}},
		}); err != nil {
			fmt.Fprintln(os.Stderr, "write synthetic model response:", err)
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
	if err := os.WriteFile(os.Args[1], []byte("http://"+listener.Addr().String()+"/v1"), 0600); err != nil {
		panic(err)
	}
	err = server.Serve(listener)
	cancel()
	shutdownErr := <-shutdownDone
	if err != nil && err != http.ErrServerClosed {
		panic(err)
	}
	if shutdownErr != nil {
		panic(fmt.Errorf("shut down demo API: %w", shutdownErr))
	}
}
