//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMainResourceSummaryClosedStdoutPreservesPageFailure(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		args       []string
		pageFails  bool
		jsonErrors bool
	}{
		{name: "successful second page"},
		{name: "explicit text successful second page", args: []string{"--format", "text"}},
		{name: "default page failure", pageFails: true},
		{name: "explicit text page failure", args: []string{"--format", "text"}, pageFails: true},
		{name: "JSON page error", args: []string{"--format", "text", "--format-error", "json"}, pageFails: true, jsonErrors: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{}, 1)
			secondPage := make(chan struct{}, 1)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/files" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Query().Get("after") == "" {
					io.WriteString(w, resourceFilePage([]string{"file_first"}, true))
					return
				}
				if r.URL.Query().Get("after") != "file_first" {
					t.Errorf("unexpected pagination cursor: %q", r.URL.Query().Get("after"))
				}
				select {
				case secondPage <- struct{}{}:
				case <-r.Context().Done():
					return
				}
				select {
				case <-release:
					if tc.pageFails {
						w.WriteHeader(http.StatusBadRequest)
						io.WriteString(w, `{"error":{"message":"synthetic second-page failure","type":"invalid_request_error"}}`)
					} else {
						io.WriteString(w, resourceFilePage(nil, false))
					}
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			defer close(release)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			args := append([]string{"-test.run=^TestMainDispatchProcess$", "--", "openai"}, tc.args...)
			args = append(args, "files", "list")
			child := exec.CommandContext(ctx, binary, args...)
			child.Env = []string{"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_API_KEY=sk-fake-resource-test", "OPENAI_BASE_URL=" + server.URL, "FORCE_COLOR=0"}
			var stderr bytes.Buffer
			child.Stderr = &stderr
			stdout, err := child.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				cancel()
				if child.ProcessState == nil {
					child.Wait()
				}
			}()
			const firstPage = "ID: file_first\nFilename: synthetic.txt\nPurpose: assistants\nBytes: 5\n"
			first := make(chan error, 1)
			partial := make([]byte, len(firstPage))
			go func() {
				_, err := io.ReadFull(stdout, partial)
				first <- err
			}()
			select {
			case err := <-first:
				if err != nil || string(partial) != firstPage {
					t.Fatalf("first summarized page = %q, error = %v", partial, err)
				}
			case <-ctx.Done():
				t.Fatal("first page was not printed while the second page was blocked")
			}
			select {
			case <-secondPage:
			case <-ctx.Done():
				t.Fatal("second-page request did not arrive")
			}
			// Only the final summary hint remains to write after this response.
			// Close the actual child stdout consumer before releasing that response.
			if err := stdout.Close(); err != nil {
				t.Fatal(err)
			}
			release <- struct{}{}
			err = child.Wait()
			if ctx.Err() != nil {
				t.Fatal("command did not finish after the second-page response")
			}
			code := 0
			if err != nil {
				exit, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatal(err)
				}
				code = exit.ExitCode()
			}
			if requests.Load() != 2 {
				t.Errorf("requests = %d; want exactly two pages", requests.Load())
			}
			if !tc.pageFails {
				if code != 0 || stderr.Len() != 0 {
					t.Fatalf("closing only the final hint must succeed quietly: exit=%d stderr=%q", code, stderr.String())
				}
				return
			}
			if code != 1 {
				t.Errorf("page failure was lost after stdout closed: exit=%d stderr=%q", code, stderr.String())
			}
			if tc.jsonErrors {
				var failure map[string]any
				if err := json.Unmarshal(stderr.Bytes(), &failure); err != nil || failure["message"] != "synthetic second-page failure" || failure["type"] != "invalid_request_error" {
					t.Errorf("explicit API error details lost: %q (%v)", stderr.String(), err)
				}
			} else if !strings.Contains(stderr.String(), "The API rejected the request.") || !strings.Contains(stderr.String(), "--format-error json") || strings.Contains(stderr.String(), "synthetic second-page failure") {
				t.Errorf("safe page-failure guidance lost: %q", stderr.String())
			}
		})
	}
}
