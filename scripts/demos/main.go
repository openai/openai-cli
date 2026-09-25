// demo-api serves fixed synthetic responses for local CLI recordings.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"strings"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: demo-api ADDRESS_FILE")
		os.Exit(2)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(serve), ReadHeaderTimeout: 5 * time.Second}
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

func serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	model := func(id string) map[string]any {
		return map[string]any{"id": id, "object": "model", "created": 1704067200, "owned_by": "demo-project"}
	}
	files := []map[string]any{
		{"id": "file_training", "object": "file", "created_at": 1704067200, "filename": "training.jsonl", "purpose": "fine-tune", "bytes": 8192, "status": "processed"},
		{"id": "file_reference", "object": "file", "created_at": 1704067100, "filename": "reference.pdf", "purpose": "assistants", "bytes": 4096, "status": "processed"},
	}
	var response any
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/files":
		response = map[string]any{"object": "list", "data": files, "has_more": false, "first_id": "file_training", "last_id": "file_reference"}
	case r.Method == http.MethodGet && r.URL.Path == "/v1/files/file_training":
		response = files[0]
	case r.Method == http.MethodPost && r.URL.Path == "/v1/images/generations":
		serveImageGeneration(w, r)
		return
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		response = map[string]any{"object": "list", "data": []any{model("demo-chat"), model("demo-image")}}
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models/demo-auth":
		w.WriteHeader(http.StatusUnauthorized)
		response = map[string]any{"error": map[string]any{"message": "The demo API key is invalid.", "type": "invalid_request_error", "param": nil, "code": "invalid_api_key"}}
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models/demo-missing":
		w.WriteHeader(http.StatusNotFound)
		response = map[string]any{"error": map[string]any{"message": "The requested demo model does not exist.", "type": "invalid_request_error", "param": "model", "code": "model_not_found"}}
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/models/"):
		response = model(strings.TrimPrefix(r.URL.Path, "/v1/models/"))
	default:
		w.WriteHeader(http.StatusNotFound)
		response = map[string]any{"error": map[string]any{"message": "No synthetic fixture for this route.", "type": "invalid_request_error", "code": "fixture_not_found"}}
	}
	_ = json.NewEncoder(w).Encode(response)
}

// Only the known synthetic request is accepted or logged. The same one-pixel
// PNG is returned to both binaries regardless of their generation defaults.
func serveImageGeneration(w http.ResponseWriter, r *http.Request) {
	var request map[string]any
	promptOnly := map[string]any{"prompt": "A tiny orange robot"}
	preset := map[string]any{
		"prompt": "A tiny orange robot", "model": "gpt-image-2.5-sunburst",
		"n": float64(1), "size": "auto", "quality": "auto", "output_format": "png",
		"background": "auto", "moderation": "auto", "partial_images": float64(0), "stream": false,
	}
	if r.Header.Get("Authorization") != "Bearer synthetic-demo-key" ||
		json.NewDecoder(r.Body).Decode(&request) != nil ||
		(!reflect.DeepEqual(request, promptOnly) && !reflect.DeepEqual(request, preset)) {
		http.Error(w, `{"error":{"message":"Expected the fixed synthetic image demo request.","type":"invalid_request_error"}}`, http.StatusBadRequest)
		return
	}
	if path := os.Getenv("DEMO_IMAGE_REQUEST_LOG"); path != "" {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			http.Error(w, `{"error":{"message":"Cannot record the synthetic request.","type":"server_error"}}`, http.StatusInternalServerError)
			return
		}
		writeErr := json.NewEncoder(file).Encode(request)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			http.Error(w, `{"error":{"message":"Cannot record the synthetic request.","type":"server_error"}}`, http.StatusInternalServerError)
			return
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"created": 1704067200,
		"data": []any{map[string]any{
			"b64_json": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP438AAAAQBAYDFKhhdAAAAAElFTkSuQmCC",
		}},
	})
}
