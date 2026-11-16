package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

func TestMainBatchInputsStoredOutputModes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/files" || r.Method != http.MethodGet {
			t.Errorf("unexpected input request: %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("after") == "" {
			fmt.Fprint(w, `{"data":[{"id":"file_first","filename":"資料-requests[1].jsonl","purpose":"batch","bytes":9007199254740993,"future":{"enabled":false,"missing":null}}],"has_more":true}`)
		} else {
			fmt.Fprint(w, `{"data":[{"id":"file_second","filename":"requests[2].jsonl","purpose":"batch","future":[1e2,"literal\\n",true]},{"id":"file_second","filename":"duplicate.jsonl","purpose":"batch"}],"has_more":false}`)
		}
	}))
	defer server.Close()
	for _, format := range []string{"json", "jsonl", "raw", "pretty", "yaml", "text", "auto"} {
		t.Run(format, func(t *testing.T) {
			got := runReadableCommand(t, server, "--format", format, "batches", "inputs", "--max-items", "-1")
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("stored input output failed: %+v", got)
			}
			for _, value := range []string{"file_first", "file_second", "9007199254740993", "future"} {
				if !strings.Contains(strings.ToLower(got.stdout), strings.ToLower(value)) {
					t.Errorf("missing %q: %s", value, got.stdout)
				}
			}
			if format == "json" || format == "jsonl" || format == "raw" {
				value := gjson.Parse(got.stdout)
				if !gjson.Valid(got.stdout) || !value.IsArray() || value.Get("#").Int() != 3 || value.Get("0.bytes").Raw != "9007199254740993" || value.Get("1.future.0").Raw != "1e2" || value.Get("2.id").Str != "file_second" {
					t.Fatalf("stored records changed: %s", got.stdout)
				}
			}
		})
	}
	for _, format := range []string{"json", "text", "yaml"} {
		t.Run("transform-"+format, func(t *testing.T) {
			got := runReadableCommand(t, server, "--format", format, "--transform", "0.id", "--raw-output", "batches", "inputs", "--max-items", "-1")
			if got.code != 0 || got.stderr != "" || strings.TrimSpace(got.stdout) != "file_first" {
				t.Fatalf("input extraction changed: %+v", got)
			}
		})
	}
}

func TestMainBatchInputsStoredEmptyAndLimit(t *testing.T) {
	for _, maximum := range []string{"0", "1"} {
		t.Run(maximum, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"data":[{"id":"file_one","purpose":"batch"},{"id":"file_two","purpose":"batch"}],"has_more":true}`)
			}))
			defer server.Close()
			got := runReadableCommand(t, server, "--format", "json", "batches", "inputs", "--max-items", maximum)
			if got.code != 0 || got.stderr != "" || calls.Load() != 1 || gjson.Get(got.stdout, "#").Raw != maximum {
				t.Fatalf("matching limit changed: %+v calls=%d", got, calls.Load())
			}
		})
	}
}

func TestMainBatchInputsStoredCombinedOutputAndAPIFailure(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Query().Get("after") == "" {
					fmt.Fprintf(w, `{"data":[{"id":"file_kept","purpose":"batch","payload":%q}],"has_more":true}`, strings.Repeat("x", 2<<20))
					return
				}
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, `{"error":{"message":"synthetic private details","type":"invalid_request_error","future":{"retained":true}}}`)
			}))
			defer server.Close()
			child := batchesWorkflowProcess(t, ctx, server, "--format", "jsonl", "--format-error", format, "batches", "inputs", "--max-items", "-1")
			child.WaitDelay = 2 * time.Second
			stdout, err := child.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdout.Close()
			var stderr bytes.Buffer
			child.Stderr = &stderr
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if child.ProcessState == nil {
					_ = child.Process.Kill()
					_ = child.Wait()
				}
			}()
			var first [1]byte
			if _, err := io.ReadFull(stdout, first[:]); err != nil {
				t.Fatal(err)
			}
			if err := stdout.Close(); err != nil {
				t.Fatal(err)
			}
			err = child.Wait()
			var exit *exec.ExitError
			if ctx.Err() != nil || !errors.As(err, &exit) || exit.ExitCode() != 1 {
				t.Fatalf("combined failure lost: %v context=%v stderr=%s", err, ctx.Err(), stderr.String())
			}
			if format == "text" {
				for _, message := range []string{"Displayed files may be incomplete", "Retry batches inputs", "HTTP 403", "Could not write the batch output", "receiving command or output destination"} {
					if !strings.Contains(stderr.String(), message) {
						t.Errorf("missing %q: %s", message, stderr.String())
					}
				}
				if strings.Count(stderr.String(), "HTTP 403") != 1 || strings.Contains(stderr.String(), "synthetic private details") {
					t.Fatalf("input failure guidance changed: %s", stderr.String())
				}
			} else if !gjson.ValidBytes(stderr.Bytes()) || !gjson.GetBytes(stderr.Bytes(), "future.retained").Bool() {
				t.Fatalf("machine API cause lost: %s", stderr.String())
			}
		})
	}
}

func TestMainBatchInputsStoredRawWhitespace(t *testing.T) {
	const nested = `{ "a" : 1e2, "array" : [1,  2] }`
	const record = "{ \"id\" : \"file_one\",\n\t\"purpose\" : \"batch\", \"future\" : " + nested + " }"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[`+record+`],"has_more":false}`)
	}))
	defer server.Close()
	for _, test := range []struct{ name, transform, want string }{
		{"list", "", "[" + record + "]\n"},
		{"record", "0", record + "\n"},
		{"nested", "0.future", nested + "\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := []string{"--format", "raw"}
			if test.transform != "" {
				args = append(args, "--transform", test.transform)
			}
			got := runReadableCommand(t, server, append(args, "batches", "inputs")...)
			if got.code != 0 || got.stderr != "" || got.stdout != test.want {
				t.Fatalf("raw bytes changed: %+v; want %q", got, test.want)
			}
		})
	}
	got := runReadableCommand(t, server, "--format", "jsonl", "batches", "inputs")
	if got.code != 0 || got.stderr != "" || strings.Count(got.stdout, "\n") != 1 || !gjson.Valid(got.stdout) || strings.Contains(got.stdout, ": ") {
		t.Fatalf("JSONL stopped compacting stored whitespace: %+v", got)
	}
}
