// files-workflow-demo-api accepts only the fixed synthetic Files workflow.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

var textFile = []byte("hello files!\n")
var binaryFile = []byte{0, 255, 13, 10, 27, 65, 128, 0, 7, 8, 9, 10}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (result error) {
	if len(os.Args) != 3 {
		return errors.New("usage: files-workflow-demo-api ADDRESS_FILE REQUEST_LOG")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	requests, err := os.OpenFile(os.Args[2], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.Join(err, listener.Close())
	}
	defer func() { result = errors.Join(result, requests.Close()) }()
	var mu sync.Mutex
	var handlerErr error
	fail := func(err error) {
		mu.Lock()
		handlerErr = errors.Join(handlerErr, err)
		mu.Unlock()
	}
	metadata := map[string]any{
		"id": "file-example", "object": "file", "bytes": len(textFile),
		"created_at": 1700000000, "filename": "upload space.txt",
		"purpose": "user_data", "status": "uploaded",
	}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { fail(r.Body.Close()) }()
		record := map[string]any{"method": r.Method, "path": r.URL.Path, "content_type": r.Header.Get("Content-Type")}
		var content []byte
		if r.Header.Get("Authorization") != "Bearer synthetic-demo-key" {
			fail(errors.New("fixture received unexpected credentials"))
			http.Error(w, "only the synthetic demo key is accepted", http.StatusForbidden)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/files":
			reader, err := r.MultipartReader()
			if err != nil {
				fail(err)
				http.Error(w, "expected synthetic multipart upload", http.StatusBadRequest)
				return
			}
			parts := make(map[string][]byte)
			for {
				part, err := reader.NextPart()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					fail(err)
					return
				}
				data, readErr := io.ReadAll(io.LimitReader(part, 1024))
				if err := errors.Join(readErr, part.Close()); err != nil {
					fail(err)
					return
				}
				name := part.FormName()
				_, duplicate := parts[name]
				if duplicate || name != "file" && name != "purpose" ||
					name == "file" && (part.FileName() != "upload space.txt" || !bytes.Equal(data, textFile)) ||
					name == "purpose" && string(data) != "user_data" {
					fail(errors.New("fixture received an unexpected upload part"))
					http.Error(w, "only fixed synthetic upload data is accepted", http.StatusBadRequest)
					return
				}
				parts[name] = data
			}
			if len(parts) != 2 {
				fail(errors.New("fixture upload omitted a required part"))
				http.Error(w, "file and purpose are required", http.StatusBadRequest)
				return
			}
			record["filename"], record["bytes"], record["purpose"] = "upload space.txt", parts["file"], string(parts["purpose"])
		case r.Method == http.MethodGet && r.URL.Path == "/v1/files/file-example":
		case r.Method == http.MethodGet && r.URL.Path == "/v1/files/file-example/content":
			content = textFile
		case r.Method == http.MethodGet && r.URL.Path == "/v1/files/file-binary/content":
			content = binaryFile
		default:
			fail(errors.New("fixture received an unexpected route"))
			http.Error(w, "only fixed synthetic Files routes are accepted", http.StatusNotFound)
			return
		}
		mu.Lock()
		err := json.NewEncoder(requests).Encode(record)
		mu.Unlock()
		if err != nil {
			fail(err)
			http.Error(w, "could not record synthetic request", http.StatusInternalServerError)
			return
		}
		if content != nil {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, err := w.Write(content)
			fail(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fail(json.NewEncoder(w).Encode(metadata))
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
		cancel()
		return errors.Join(err, listener.Close(), <-shutdownDone)
	}
	serveErr := server.Serve(listener)
	cancel()
	shutdownErr := <-shutdownDone
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	mu.Lock()
	defer mu.Unlock()
	return errors.Join(serveErr, shutdownErr, handlerErr)
}
