package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const batchesWorkflowCounts = `{"total":3,"completed":3,"failed":0}`
const batchesWorkflowContent = "{\"custom_id\":\"request-2\",\"response\":{\"status_code\":200}}\r\n{\"custom_id\":\"request-1\",\"error\":{\"code\":\"synthetic_error\"}}\n"

func batchesWorkflowResponse(status, counts string) string {
	fields := `{"id":"batch_synthetic","object":"batch","status":` + fmt.Sprintf("%q", status) + `,"input_file_id":"file_input","output_file_id":"file_output","error_file_id":"file_error"`
	if counts != "" {
		fields += `,"request_counts":` + counts
	}
	return fields + `,"future_field":{"exact_integer":9007199254740993}}`
}

func TestMainBatchesWaitFinalOutputModes(t *testing.T) {
	for _, mode := range []string{"", "text", "json", "jsonl", "raw", "yaml"} {
		t.Run(mode, func(t *testing.T) {
			states := []string{"validating", "in_progress", "finalizing", "cancelling", "completed"}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/batches/batch_synthetic" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				index := int(requests.Add(1)) - 1
				if index >= len(states) {
					t.Error("polled after terminal state")
					index = len(states) - 1
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, batchesWorkflowResponse(states[index], batchesWorkflowCounts))
			}))
			defer server.Close()
			args := []string{"batches", "retrieve", "batch_synthetic", "--wait", "--poll-interval", "1ms"}
			if mode != "" {
				args = append(args, "--format", mode)
			}
			got := runReadableCommand(t, server, args...)
			if got.code != 0 || got.stderr != "" || requests.Load() != int32(len(states)) {
				t.Fatalf("result=%+v requests=%d", got, requests.Load())
			}
			for _, state := range states[:len(states)-1] {
				if strings.Contains(got.stdout, state) {
					t.Fatalf("intermediate response reached stdout: %q", got.stdout)
				}
			}
			if !strings.Contains(got.stdout, "completed") || !strings.Contains(got.stdout, "9007199254740993") {
				t.Fatalf("final response lost fields: %q", got.stdout)
			}
			if mode == "json" || mode == "jsonl" || mode == "raw" {
				var value map[string]any
				if err := json.Unmarshal([]byte(got.stdout), &value); err != nil || value["status"] != "completed" {
					t.Fatalf("stdout must contain exactly one final object: %q (%v)", got.stdout, err)
				}
			}
			if mode == "raw" && got.stdout != batchesWorkflowResponse("completed", batchesWorkflowCounts)+"\n" {
				t.Fatalf("raw API bytes changed: %q", got.stdout)
			}
		})
	}
}

func TestMainBatchesWaitTerminalExitStatuses(t *testing.T) {
	for _, tc := range []struct {
		name, state, counts string
		code                int
	}{
		{"success", "completed", batchesWorkflowCounts, 0},
		{"partial", "completed", `{"total":3,"completed":2,"failed":1}`, 1},
		{"failed", "failed", `{"total":3,"completed":0,"failed":0}`, 1},
		{"expired", "expired", `{"total":3,"completed":1,"failed":1}`, 1},
		{"cancelled", "cancelled", `{"total":3,"completed":1,"failed":0}`, 1},
		{"unknown", "future_state", batchesWorkflowCounts, 1},
		{"missing_counts", "completed", "", 1},
		{"null_counts", "completed", "null", 1},
		{"incomplete_counts", "completed", `{"total":3,"completed":3}`, 1},
		{"negative_counts", "completed", `{"total":3,"completed":3,"failed":-1}`, 1},
		{"inconsistent_counts", "completed", `{"total":3,"completed":2,"failed":0}`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet {
					t.Errorf("wait must never mutate remote work: %s", r.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, batchesWorkflowResponse(tc.state, tc.counts))
			}))
			defer server.Close()
			got := runReadableCommand(t, server, "batches", "retrieve", "--batch-id", "batch_synthetic", "--wait", "--format", "json")
			if got.code != tc.code || requests.Load() != 1 || !json.Valid([]byte(got.stdout)) {
				t.Fatalf("result=%+v requests=%d; want exit %d and one final object", got, requests.Load(), tc.code)
			}
			if tc.code != 0 && !json.Valid([]byte(got.stderr)) || tc.code == 0 && got.stderr != "" {
				t.Fatalf("invalid diagnostic channel: %+v", got)
			}
		})
	}
}

func TestMainBatchesWorkflowValidationBeforeHTTP(t *testing.T) {
	for _, args := range [][]string{
		{"retrieve", "batch_synthetic", "--poll-interval", "1s"},
		{"retrieve", "batch_synthetic", "--wait-timeout", "1s"},
		{"retrieve", "batch_synthetic", "--wait=false", "--poll-interval", "1s"},
		{"retrieve", "batch_synthetic", "--wait", "--poll-interval", "0"},
		{"retrieve", "batch_synthetic", "--wait", "--poll-interval", "-1s"},
		{"retrieve", "batch_synthetic", "--wait", "--wait-timeout", "-1s"},
		{"retrieve", "batch_synthetic", "--wait", "--wait-timeout", "invalid"},
		{"retrieve", "--wait"},
		{"retrieve", "batch_synthetic", "extra", "--wait"},
		{"download", "batch_synthetic"},
		{"download", "--output", "-"},
		{"download", "batch_synthetic", "--file", "invalid", "--output", "-"},
		{"download", "batch_synthetic", "extra", "--output", "-"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				http.Error(w, "unexpected HTTP", http.StatusBadRequest)
			}))
			defer server.Close()
			got := runReadableCommand(t, server, append([]string{"batches"}, args...)...)
			if got.code != 1 || got.stdout != "" || got.stderr == "" || requests.Load() != 0 {
				t.Fatalf("invalid arguments reached HTTP or succeeded: result=%+v requests=%d", got, requests.Load())
			}
		})
	}
}

func TestMainBatchesWaitPreservesInputsAndOneShotRetrieve(t *testing.T) {
	for _, tc := range []struct {
		name, stdin string
		args        []string
		want        string
	}{
		{"stdin_json", `{"batch_id":"batch_synthetic"}`, []string{"--wait", "--transform", "output_file_id", "--raw-output"}, "file_output\n"},
		{"stdin_yaml", "batch_id: batch_synthetic\n", []string{"--wait", "--transform", "output_file_id", "--raw-output"}, "file_output\n"},
		{"explicit_wins", `{"batch_id":"batch_other"}`, []string{"--batch-id", "batch_synthetic", "--wait", "--transform", "output_file_id", "--raw-output"}, "file_output\n"},
		{"false_delegates", "", []string{"batch_synthetic", "--wait=false", "--transform", "status", "--raw-output"}, "completed\n"},
		{"absent_delegates", "", []string{"batch_synthetic", "--transform", "status", "--raw-output"}, "completed\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/batches/batch_synthetic" || r.Header.Get("OpenAI-Project") != "proj_synthetic" || r.Header.Get("OpenAI-Organization") != "org_synthetic" || r.Header.Get("X-Synthetic") != "kept" {
					t.Errorf("request options changed: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				counts := batchesWorkflowCounts
				if strings.Contains(tc.name, "delegates") {
					counts = `{"total":3,"completed":2,"failed":1}`
				}
				fmt.Fprint(w, batchesWorkflowResponse("completed", counts))
			}))
			defer server.Close()
			var stdin *os.File
			if tc.stdin != "" {
				name := filepath.Join(t.TempDir(), "stdin.json")
				if err := os.WriteFile(name, []byte(tc.stdin), 0600); err != nil {
					t.Fatal(err)
				}
				var err error
				stdin, err = os.Open(name)
				if err != nil {
					t.Fatal(err)
				}
				defer stdin.Close()
			}
			args := []string{"openai", "--project", "proj_synthetic", "--organization", "org_synthetic", "--header", "X-Synthetic: kept", "batches", "retrieve"}
			args = append(args, tc.args...)
			got := runMainDispatchWithStdin(t, "bash", []string{"OPENAI_API_KEY=sk-fake-batches-test", "OPENAI_BASE_URL=" + server.URL, "FORCE_COLOR=0"}, stdin, args...)
			if got.code != 0 || got.stderr != "" || got.stdout != tc.want || requests.Load() != 1 {
				t.Fatalf("result=%+v requests=%d", got, requests.Load())
			}
		})
	}
}

func TestMainBatchesWaitAPIErrorsAndSDKRetries(t *testing.T) {
	for _, tc := range []struct {
		name       string
		statuses   []int
		retryAfter string
		code       int
	}{
		{"auth", []int{401}, "", 1},
		{"permission", []int{403}, "", 1},
		{"missing", []int{404}, "", 1},
		{"validation", []int{400}, "", 1},
		{"conflict", []int{409, 409, 409}, "0", 1},
		{"rate_limit", []int{429, 429, 429}, "0", 1},
		{"server_error", []int{500, 500, 500}, "0", 1},
		{"transient_then_success", []int{429, 500, 200}, "0", 0},
		{"exhausted", []int{503, 503, 503}, "0", 1},
		{"excessive_retry_after", []int{429}, "3600", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				index := int(requests.Add(1)) - 1
				if r.Method != http.MethodGet || index >= len(tc.statuses) {
					t.Errorf("unexpected retry/mutation: %s attempt %d", r.Method, index)
					index = len(tc.statuses) - 1
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.retryAfter != "" {
					w.Header().Set("Retry-After", tc.retryAfter)
				}
				w.WriteHeader(tc.statuses[index])
				if tc.statuses[index] == 200 {
					fmt.Fprint(w, batchesWorkflowResponse("completed", batchesWorkflowCounts))
				} else {
					io.WriteString(w, `{"error":{"message":"Synthetic API failure.","type":"invalid_request_error","code":"synthetic_error"}}`)
				}
			}))
			defer server.Close()
			got := runReadableCommand(t, server, "batches", "retrieve", "batch_synthetic", "--wait", "--format", "json")
			if got.code != tc.code || requests.Load() != int32(len(tc.statuses)) {
				t.Fatalf("result=%+v requests=%d", got, requests.Load())
			}
			if tc.code != 0 && (got.stdout != "" || !json.Valid([]byte(got.stderr)) || !strings.Contains(got.stderr, "synthetic_error")) {
				t.Fatalf("API error channels changed: %+v", got)
			}
		})
	}
}

func TestMainBatchesWaitTimeoutBoundsPollAndRetry(t *testing.T) {
	for _, phase := range []string{"poll", "request", "retry"} {
		t.Run(phase, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet {
					t.Error("timeout must not cancel remote work")
				}
				if phase == "request" {
					<-r.Context().Done()
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if phase == "retry" {
					w.Header().Set("Retry-After", "60")
					w.WriteHeader(http.StatusTooManyRequests)
					io.WriteString(w, `{"error":{"message":"Synthetic retry."}}`)
					return
				}
				fmt.Fprint(w, batchesWorkflowResponse("in_progress", batchesWorkflowCounts))
			}))
			defer server.Close()
			got := runReadableCommand(t, server, "batches", "retrieve", "batch_synthetic", "--wait", "--poll-interval", "60s", "--wait-timeout", "250ms", "--format", "json")
			if got.code != 124 || got.stdout != "" || !json.Valid([]byte(got.stderr)) || requests.Load() != 1 {
				t.Fatalf("timeout result=%+v requests=%d", got, requests.Load())
			}
		})
	}
}

// This helper uses the production main in a fresh process, with synthetic credentials only.
func batchesWorkflowProcess(t *testing.T, ctx context.Context, server *httptest.Server, args ...string) *exec.Cmd {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(ctx, binary, append([]string{"-test.run=^TestMainDispatchProcess$", "--", "openai"}, args...)...)
	child.Env = []string{"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_API_KEY=sk-fake-batches-test", "OPENAI_BASE_URL=" + server.URL, "FORCE_COLOR=0", "GOMAXPROCS=2"}
	return child
}

func TestMainBatchesWaitInterruptNeverCancelsRemoteBatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Process.Signal cannot send os.Interrupt on Windows")
	}
	for _, phase := range []string{"poll", "request"} {
		t.Run(phase, func(t *testing.T) {
			entered := make(chan struct{})
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) != 1 || r.Method != http.MethodGet {
					t.Errorf("interrupt caused another request: %s %s", r.Method, r.URL)
				}
				if phase == "poll" {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, batchesWorkflowResponse("in_progress", batchesWorkflowCounts))
					w.(http.Flusher).Flush()
				}
				close(entered)
				if phase == "request" {
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			child := batchesWorkflowProcess(t, ctx, server, "batches", "retrieve", "batch_synthetic", "--wait", "--poll-interval", "60s", "--format", "json")
			var stdout, stderr bytes.Buffer
			child.Stdout, child.Stderr = &stdout, &stderr
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("batch request never started")
			}
			if err := child.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			err := child.Wait()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 130 || stdout.Len() != 0 || !json.Valid(stderr.Bytes()) || requests.Load() != 1 {
				t.Fatalf("interrupt: err=%v stdout=%q stderr=%q requests=%d", err, stdout.String(), stderr.String(), requests.Load())
			}
		})
	}
}

func TestMainBatchesDownloadSelectedBytesAndReceipts(t *testing.T) {
	for _, selected := range []string{"output", "error", "input"} {
		for _, destination := range []string{"-", "file"} {
			t.Run(selected+"/"+destination, func(t *testing.T) {
				var mu sync.Mutex
				var paths []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					paths = append(paths, r.URL.Path)
					mu.Unlock()
					if r.Method != http.MethodGet {
						t.Error("download must not mutate remote work")
					}
					if r.URL.Path == "/batches/batch_synthetic" {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, batchesWorkflowResponse("cancelled", `{"total":3,"completed":1,"failed":1}`))
					} else {
						w.Header().Set("Content-Type", "application/jsonl")
						io.WriteString(w, batchesWorkflowContent)
					}
				}))
				defer server.Close()
				output := destination
				if destination == "file" {
					output = filepath.Join(t.TempDir(), "synthetic result.jsonl")
				}
				got := runReadableCommand(t, server, "batches", "download", "batch_synthetic", "--file", selected, "--output", output, "--format", "json")
				mu.Lock()
				actualPaths := append([]string(nil), paths...)
				mu.Unlock()
				wantPaths := []string{"/batches/batch_synthetic", "/files/file_" + selected + "/content"}
				if got.code != 0 || got.stderr != "" || !reflect.DeepEqual(actualPaths, wantPaths) {
					t.Fatalf("result=%+v paths=%v", got, actualPaths)
				}
				if destination == "-" {
					if got.stdout != batchesWorkflowContent {
						t.Fatalf("JSONL bytes changed: %q", got.stdout)
					}
				} else {
					contents, err := os.ReadFile(output)
					if err != nil || string(contents) != batchesWorkflowContent || !json.Valid([]byte(got.stdout)) {
						t.Fatalf("download or receipt changed: contents=%q error=%v result=%+v", contents, err, got)
					}
					entries, err := os.ReadDir(filepath.Dir(output))
					if err != nil || len(entries) != 1 {
						t.Fatalf("owned temporary files remain: %v (%v)", entries, err)
					}
				}
			})
		}
	}
}

func TestMainBatchesDownloadMissingAndInterruptedFiles(t *testing.T) {
	for _, failure := range []string{"absent", "expired", "truncated", "existing", "directory", "symlink"} {
		t.Run(failure, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "result.jsonl")
			const original = "preserve existing bytes"
			switch failure {
			case "existing":
				if err := os.WriteFile(output, []byte(original), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(output, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if runtime.GOOS == "windows" {
					t.Skip("symlink creation can require privileges on Windows")
				}
				target := filepath.Join(t.TempDir(), "original")
				if err := os.WriteFile(target, []byte(original), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, output); err != nil {
					t.Fatal(err)
				}
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path == "/batches/batch_synthetic" {
					w.Header().Set("Content-Type", "application/json")
					body := batchesWorkflowResponse("expired", batchesWorkflowCounts)
					if failure == "absent" {
						body = strings.Replace(body, `"output_file_id":"file_output"`, `"output_file_id":null`, 1)
					}
					fmt.Fprint(w, body)
					return
				}
				if failure == "expired" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusNotFound)
					io.WriteString(w, `{"error":{"message":"Synthetic file expired.","code":"file_not_found"}}`)
					return
				}
				w.Header().Set("Content-Type", "application/jsonl")
				if failure == "truncated" {
					w.Header().Set("Content-Length", "4096")
				}
				io.WriteString(w, batchesWorkflowContent)
			}))
			defer server.Close()
			got := runReadableCommand(t, server, "batches", "download", "batch_synthetic", "--output", output, "--format", "json")
			if got.code != 1 || got.stdout != "" || !json.Valid([]byte(got.stderr)) {
				t.Fatalf("download failure was hidden: %+v", got)
			}
			if failure == "absent" && requests.Load() != 1 {
				t.Fatalf("absent file caused content request: %d", requests.Load())
			}
			entries, err := os.ReadDir(filepath.Dir(output))
			if err != nil {
				t.Fatal(err)
			}
			if failure == "existing" || failure == "symlink" {
				contents, err := os.ReadFile(output)
				if err != nil || string(contents) != original || len(entries) != 1 {
					t.Fatalf("existing destination changed: %q %v entries=%v", contents, err, entries)
				}
			} else if failure == "directory" {
				info, err := os.Stat(output)
				if err != nil || !info.IsDir() || len(entries) != 1 {
					t.Fatalf("directory changed: %v entries=%v", err, entries)
				}
			} else if len(entries) != 0 {
				t.Fatalf("failed download published partial content or leaked temporary files: %v", entries)
			}
		})
	}
}

func TestMainBatchesDownloadStreamsBeforeNextChunk(t *testing.T) {
	const first = "{\"custom_id\":\"request-first\"}\n"
	const second = "{\"custom_id\":\"request-second\"}\n"
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/batches/batch_synthetic" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, batchesWorkflowResponse("completed", batchesWorkflowCounts))
			return
		}
		w.Header().Set("Content-Type", "application/jsonl")
		io.WriteString(w, first)
		w.(http.Flusher).Flush()
		select {
		case <-release:
			io.WriteString(w, second)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	child := batchesWorkflowProcess(t, ctx, server, "batches", "download", "batch_synthetic", "--output", "-")
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	child.Stderr = &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	if err != nil || line != first {
		t.Fatalf("first output did not precede second chunk: line=%q error=%v", line, err)
	}
	once.Do(func() { close(release) })
	rest, err := io.ReadAll(reader)
	if err != nil || string(rest) != second {
		t.Fatalf("remaining bytes changed: %q (%v)", rest, err)
	}
	if err := child.Wait(); err != nil || stderr.Len() != 0 {
		t.Fatalf("download process failed: %v stderr=%q", err, stderr.String())
	}
}

func TestMainBatchesDownloadLargeFile(t *testing.T) {
	// This single 65 MiB JSONL line catches accidental scanner or response-size caps.
	const chunks = 65
	chunk := strings.Repeat("x", 1<<20)
	prefix, suffix := `{"custom_id":"large-synthetic","body":"`, "\"}\n"
	want := sha256.New()
	io.WriteString(want, prefix)
	for range chunks {
		io.WriteString(want, chunk)
	}
	io.WriteString(want, suffix)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/batches/batch_synthetic" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, batchesWorkflowResponse("completed", batchesWorkflowCounts))
			return
		}
		w.Header().Set("Content-Type", "application/jsonl")
		io.WriteString(w, prefix)
		for range chunks {
			if _, err := io.WriteString(w, chunk); err != nil {
				return
			}
		}
		io.WriteString(w, suffix)
	}))
	defer server.Close()
	output := filepath.Join(t.TempDir(), "large.jsonl")
	got := runReadableCommand(t, server, "batches", "download", "batch_synthetic", "--output", output, "--format", "json")
	if got.code != 0 || got.stderr != "" || !json.Valid([]byte(got.stdout)) {
		t.Fatalf("large download failed: %+v", got)
	}
	file, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	actual := sha256.New()
	size, err := io.Copy(actual, file)
	if err != nil || size != int64(len(prefix)+chunks*len(chunk)+len(suffix)) || !bytes.Equal(actual.Sum(nil), want.Sum(nil)) {
		t.Fatalf("large content changed: size=%d error=%v", size, err)
	}
}

func TestMainBatchesDownloadStdinAndDefaultSelection(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/batches/batch_synthetic" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, batchesWorkflowResponse("expired", batchesWorkflowCounts))
			return
		}
		io.WriteString(w, batchesWorkflowContent)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	child := batchesWorkflowProcess(t, ctx, server, "batches", "download", "--output", "-", "--format", "raw")
	child.Stdin = strings.NewReader(`{"batch_id":"batch_synthetic"}`)
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	if err := child.Run(); err != nil || stdout.String() != batchesWorkflowContent || stderr.Len() != 0 {
		t.Fatalf("stdin download changed: %v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(paths, []string{"/batches/batch_synthetic", "/files/file_output/content"}) {
		t.Fatalf("default selection or request count changed: %v", paths)
	}
}

func TestMainBatchesDownloadConcurrentDestination(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var contents atomic.Int32
	ready := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/batches/batch_synthetic" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, batchesWorkflowResponse("completed", batchesWorkflowCounts))
			return
		}
		if contents.Add(1) == 2 {
			close(ready)
		}
		select {
		case <-ready:
			io.WriteString(w, batchesWorkflowContent)
		case <-ctx.Done():
		}
	}))
	defer server.Close()
	output := filepath.Join(t.TempDir(), "winner.jsonl")
	children := [2]*exec.Cmd{}
	var stdout, stderr [2]bytes.Buffer
	for i := range children {
		children[i] = batchesWorkflowProcess(t, ctx, server, "batches", "download", "batch_synthetic", "--output", output, "--format", "json")
		children[i].Stdout, children[i].Stderr = &stdout[i], &stderr[i]
		if err := children[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	succeeded := 0
	for i, child := range children {
		err := child.Wait()
		if err == nil {
			succeeded++
			if !json.Valid(stdout[i].Bytes()) || stderr[i].Len() != 0 {
				t.Fatalf("winner output invalid: stdout=%q stderr=%q", stdout[i].String(), stderr[i].String())
			}
		} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || stdout[i].Len() != 0 || !json.Valid(stderr[i].Bytes()) {
			t.Fatalf("loser output invalid: %v stdout=%q stderr=%q", err, stdout[i].String(), stderr[i].String())
		}
	}
	data, err := os.ReadFile(output)
	if succeeded != 1 || err != nil || string(data) != batchesWorkflowContent {
		t.Fatalf("concurrent publication changed content: winners=%d data=%q error=%v", succeeded, data, err)
	}
	entries, err := os.ReadDir(filepath.Dir(output))
	if err != nil || len(entries) != 1 {
		t.Fatalf("concurrent staging files remain: %v (%v)", entries, err)
	}
}

func TestMainBatchesDownloadBrokenPipeIsFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/batches/batch_synthetic" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, batchesWorkflowResponse("completed", batchesWorkflowCounts))
			return
		}
		io.WriteString(w, batchesWorkflowContent)
		w.(http.Flusher).Flush()
		select {
		case <-release:
			chunk := strings.Repeat("x", 32<<10)
			for range 32 {
				if _, err := io.WriteString(w, chunk); err != nil {
					return
				}
			}
		case <-ctx.Done():
		}
	}))
	defer server.Close()
	child := batchesWorkflowProcess(t, ctx, server, "batches", "download", "batch_synthetic", "--output", "-")
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	child.Stderr = &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(stdout).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if err := stdout.Close(); err != nil {
		t.Fatal(err)
	}
	once.Do(func() { close(release) })
	if err := child.Wait(); err == nil || ctx.Err() != nil {
		t.Fatalf("closed stdout must fail promptly: %v context=%v stderr=%q", err, ctx.Err(), stderr.String())
	}
}

func TestMainBatchesWorkflowHelpWithoutCredentials(t *testing.T) {
	for _, args := range [][]string{
		{"batches", "retrieve", "--help"},
		{"help", "--all", "batches", "retrieve"},
		{"batches", "download", "--help"},
		{"help", "--all", "batches", "download"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				http.Error(w, "help must stay offline", 500)
			}))
			defer server.Close()
			got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=" + server.URL}, append([]string{"openai"}, args...)...)
			if got.code != 0 || got.stderr != "" || requests.Load() != 0 {
				t.Fatalf("help failed or reached HTTP: result=%+v requests=%d", got, requests.Load())
			}
			if strings.Contains(strings.Join(args, " "), "download") && !strings.Contains(got.stdout, "--output") {
				t.Fatalf("download help lost required output: %q", got.stdout)
			}
			if strings.Contains(strings.Join(args, " "), "retrieve") && strings.Contains(strings.Join(args, " "), "--all") && !strings.Contains(got.stdout, "--wait") {
				t.Fatalf("full retrieve help omitted wait: %q", got.stdout)
			}
		})
	}
}
