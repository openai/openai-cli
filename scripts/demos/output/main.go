// output-demo-api serves only fixed synthetic model and stream responses.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

//go:embed fixtures.json
var fixture []byte

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: output-demo-api ADDRESS_FILE REQUEST_LOG")
		os.Exit(2)
	}
	var payload struct {
		Model  json.RawMessage   `json:"model"`
		Events []json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal(fixture, &payload); err != nil {
		panic(err)
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
			panic(fmt.Errorf("close synthetic request log: %w", err))
		}
	}()
	var logMu sync.Mutex
	var failed atomic.Bool
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-demo-key" || r.URL.RawQuery != "" {
			failed.Store(true)
			http.Error(w, "only the synthetic demo request is accepted", http.StatusBadRequest)
			return
		}
		record := map[string]any{"method": r.Method, "path": r.URL.Path}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models/demo-model":
			bodyBytes, bodyErr := io.Copy(io.Discard, r.Body)
			if bodyErr != nil {
				failed.Store(true)
				http.Error(w, "could not read synthetic request body", http.StatusBadRequest)
				return
			}
			if bodyBytes != 0 {
				failed.Store(true)
				http.Error(w, "unexpected synthetic GET request body", http.StatusBadRequest)
				return
			}
		case r.Method == http.MethodPost && r.URL.Path == "/v1/responses":
			var body map[string]any
			decoder := json.NewDecoder(r.Body)
			if err := decoder.Decode(&body); err != nil || len(body) != 3 ||
				body["model"] != "demo-model" || body["input"] != "Say hi" || body["stream"] != true {
				failed.Store(true)
				http.Error(w, "unexpected synthetic request body", http.StatusBadRequest)
				return
			}
			var extra any
			if err := decoder.Decode(&extra); err != io.EOF {
				failed.Store(true)
				http.Error(w, "unexpected trailing request data", http.StatusBadRequest)
				return
			}
			record["body"] = body
		default:
			failed.Store(true)
			http.Error(w, "only the synthetic demo routes are accepted", http.StatusNotFound)
			return
		}
		// Record validated fixture fields only. Never log request headers.
		logMu.Lock()
		err := json.NewEncoder(requests).Encode(record)
		logMu.Unlock()
		if err != nil {
			failed.Store(true)
			http.Error(w, "could not record synthetic request", http.StatusInternalServerError)
			return
		}
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write(payload.Model); err != nil {
				failed.Store(true)
			}
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for index, event := range payload.Events {
			if index > 0 {
				timer := time.NewTimer(2 * time.Second)
				select {
				case <-timer.C:
				case <-r.Context().Done():
					timer.Stop()
					failed.Store(true)
					return
				}
			}
			data, err := json.Marshal(event)
			if err != nil {
				failed.Store(true)
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				failed.Store(true)
				return
			}
			if err := http.NewResponseController(w).Flush(); err != nil {
				failed.Store(true)
				return
			}
		}
		if _, err := io.WriteString(w, "data: [DONE]\n\n"); err != nil {
			failed.Store(true)
		}
	})
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		shutdownDone <- server.Shutdown(shutdown)
	}()
	if err := os.WriteFile(os.Args[1], []byte("http://"+listener.Addr().String()+"/v1"), 0600); err != nil {
		panic(err)
	}
	serveErr := server.Serve(listener)
	cancel()
	shutdownErr := <-shutdownDone
	if serveErr != nil && serveErr != http.ErrServerClosed {
		panic(serveErr)
	}
	if shutdownErr != nil {
		panic(fmt.Errorf("shut down synthetic API: %w", shutdownErr))
	}
	if failed.Load() {
		panic("synthetic fixture did not complete every request")
	}
}
