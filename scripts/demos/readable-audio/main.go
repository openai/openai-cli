// audio-demo-api serves a fixed, delayed synthetic transcription event stream.
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
	"syscall"
	"time"
)

//go:embed events.json
var fixture []byte

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: audio-demo-api ADDRESS_FILE REQUEST_LOG")
		os.Exit(2)
	}
	var events []json.RawMessage
	if err := json.Unmarshal(fixture, &events); err != nil {
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
			panic(fmt.Errorf("close demo request log: %w", err))
		}
	}()
	var logMu sync.Mutex
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/audio/transcriptions" || r.Header.Get("Authorization") != "Bearer synthetic-demo-key" {
			http.Error(w, "only the synthetic demo request is accepted", http.StatusNotFound)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, "invalid synthetic multipart request", http.StatusBadRequest)
			return
		}
		defer r.MultipartForm.RemoveAll()
		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "missing synthetic fixture", http.StatusBadRequest)
			return
		}
		data, err := io.ReadAll(io.LimitReader(file, 1024))
		file.Close()
		if err != nil || string(data) != "synthetic audio fixture\n" || header.Filename != "sample.wav" ||
			r.FormValue("model") != "demo-model" || r.FormValue("stream") != "true" {
			http.Error(w, "unexpected synthetic request", http.StatusBadRequest)
			return
		}
		body := map[string]any{"model": "demo-model", "file": "sample.wav", "stream": true}
		// Log only this validated synthetic request, never request headers.
		logMu.Lock()
		err = json.NewEncoder(requests).Encode(map[string]any{"method": r.Method, "path": r.URL.Path, "body": body})
		logMu.Unlock()
		if err != nil {
			http.Error(w, "could not record synthetic request", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		for _, event := range events {
			timer := time.NewTimer(650 * time.Millisecond)
			select {
			case <-r.Context().Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			data, err := json.Marshal(event)
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
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
