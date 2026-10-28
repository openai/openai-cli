package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestMainBatchSubmitUploadsPreparedSnapshot(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit=%t", explicit), func(t *testing.T) {
			const size = 8 << 20
			before := `{"url":"/v1/responses","body":{"input":"` + strings.Repeat("a", size) + `"}}` + "\n"
			after := strings.ReplaceAll(before, "aaa", "bbb")
			path := filepath.Join(t.TempDir(), "original input 資料.jsonl")
			if err := os.WriteFile(path, []byte(before), 0600); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var requests []string
			var uploaded []byte
			var filename string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				requests = append(requests, r.Method+" "+r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/files":
					if err := os.WriteFile(path, []byte(after), 0600); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					reader, err := r.MultipartReader()
					if err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					for {
						part, err := reader.NextPart()
						if err == io.EOF {
							break
						}
						if err != nil {
							t.Error(err)
							w.WriteHeader(400)
							return
						}
						if part.FormName() == "file" {
							filename = part.FileName()
							hash := sha256.New()
							count, err := io.Copy(hash, part)
							if err != nil || count != int64(len(before)) {
								t.Errorf("upload read: bytes=%d error=%v", count, err)
							}
							uploaded = hash.Sum(nil)
						}
						if err := part.Close(); err != nil {
							t.Error(err)
						}
					}
					io.WriteString(w, `{"id":"file_snapshot"}`)
				case "/batches":
					io.WriteString(w, `{"id":"batch_snapshot","status":"validating"}`)
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(400)
				}
			}))
			defer server.Close()
			args := []string{"--format", "json", "batches", "submit", path}
			if explicit {
				args = append(args, "--endpoint", "/v1/responses")
			}
			got := runReadableCommand(t, server, args...)
			if got.code != 0 || !strings.Contains(got.stdout, "batch_snapshot") || got.stderr != "" {
				t.Fatalf("submission failed: %+v", got)
			}
			mu.Lock()
			defer mu.Unlock()
			want := sha256.Sum256([]byte(before))
			if fmt.Sprintf("%x", uploaded) != fmt.Sprintf("%x", want) {
				t.Fatal("uploaded bytes changed after snapshot preparation")
			}
			if filename != filepath.Base(path) {
				t.Fatalf("upload filename changed: %q", filename)
			}
			if strings.Join(requests, ",") != "POST /files,POST /batches" {
				t.Fatalf("submission repeated a mutation: %v", requests)
			}
		})
	}
}
