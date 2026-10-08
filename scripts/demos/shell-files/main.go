// Synthetic loopback API for the shell/file comparison recording.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) != 2 {
		return errors.New("usage: shell-files-demo ADDRESS_FILE")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/responses":
			var body struct {
				Input string `json:"input"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid synthetic request", 400)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			response := map[string]any{"id": "resp_synthetic", "object": "response", "status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": body.Input}}}}}
			if err := json.NewEncoder(w).Encode(response); err != nil {
				return
			}
		case "/files/file-synthetic/content":
			w.Header().Set("Content-Type", "application/octet-stream")
			if _, err := w.Write([]byte{0, 255, 254, 'o', 'k', '\n'}); err != nil {
				return
			}
		case "/files/file-truncated/content":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", "100")
			if _, err := io.WriteString(w, "PARTIAL"); err != nil {
				return
			}
		default:
			http.Error(w, "unexpected synthetic route", 404)
		}
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	if err := os.WriteFile(os.Args[1], []byte("http://"+listener.Addr().String()), 0600); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			return err
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	return nil
}
