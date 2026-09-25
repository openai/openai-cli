// image-model-demo-api serves synthetic metadata only. It never generates images.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: image-model-demo-api ADDRESS_FILE REQUEST_LOG")
		os.Exit(2)
	}
	requests, err := os.OpenFile(os.Args[2], os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		panic(err)
	}
	defer requests.Close()
	requestLog := log.New(requests, "", 0)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	server := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serveModel(w, r, requestLog) }),
		ReadHeaderTimeout: 5 * time.Second,
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		_ = server.Shutdown(shutdown)
	}()
	if err := os.WriteFile(os.Args[1], []byte("http://"+listener.Addr().String()), 0600); err != nil {
		panic(err)
	}
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		panic(err)
	}
}

func serveModel(w http.ResponseWriter, r *http.Request, requests *log.Logger) {
	w.Header().Set("Content-Type", "application/json")
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if r.Method != http.MethodGet || len(parts) != 4 || parts[1] != "v1" || parts[2] != "models" ||
		(parts[0] != "normal" && parts[0] != "partial" && parts[0] != "timeout") || !knownModel(parts[3]) {
		// Record no caller-controlled URL, header, body, or credential data.
		requests.Print("rejected route")
		writeError(w, http.StatusNotFound)
		return
	}
	scenario, id := parts[0], parts[3]
	requests.Printf("%s metadata %s", scenario, id)
	if scenario == "partial" && id == "gpt-image-1" {
		writeError(w, http.StatusServiceUnavailable)
		return
	}
	if scenario == "timeout" && id == "gpt-image-1" {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(10 * time.Second):
		}
	}
	if id == "chatgpt-image-latest" {
		writeError(w, http.StatusNotFound)
		return
	}
	model := map[string]any{"id": id, "object": "model", "created": 1704067200, "owned_by": "synthetic-demo-project"}
	if id == "dall-e-2" || id == "dall-e-3" {
		model["shutdown_date"] = "2000-01-01" // Deliberately synthetic, always in the past.
	}
	_ = json.NewEncoder(w).Encode(model)
}

func knownModel(id string) bool {
	switch id {
	case "gpt-image-2.5-sunburst", "gpt-image-2.5-flare", "gpt-image-2", "gpt-image-1.5", "gpt-image-1", "gpt-image-1-mini",
		"chatgpt-image-latest", "dall-e-3", "dall-e-2", "gpt-image-2.5-sunburst-2026-09-08", "gpt-image-2.5-flare-2026-09-08", "gpt-image-2-2026-04-21":
		return true
	}
	return false
}

func writeError(w http.ResponseWriter, status int) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
		"message": "Synthetic model metadata fixture.", "type": "synthetic_error", "code": "synthetic_metadata_error",
	}})
}
