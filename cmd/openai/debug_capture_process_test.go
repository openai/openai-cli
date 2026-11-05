package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMainDebugCaptureBodyPreservesFormatsAndRequests(t *testing.T) {
	const response = `{"id":"resp_synthetic","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"synthetic-private-answer"}]}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3},"synthetic-private-key":"synthetic-private-value"}`
	for _, format := range []string{"auto", "text", "json", "jsonl", "raw", "yaml", "pretty", "explore"} {
		t.Run(format, func(t *testing.T) {
			hashes := make(chan [32]byte, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				var request map[string]any
				if err := json.Unmarshal(body, &request); err != nil || request["input"] != "synthetic-private-prompt" || request["model"] != "fake-model" {
					t.Errorf("request changed: body=%q error=%v", body, err)
				}
				select {
				case hashes <- sha256.Sum256(body):
				default:
					t.Error("unexpected extra request")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Private", "synthetic-private-header")
				io.WriteString(w, response)
			}))
			t.Cleanup(server.Close)
			args := []string{"--format", format, "responses", "create", "--model", "fake-model", "--input", "synthetic-private-prompt"}
			control := runDebugCaptureCommand(t, server, args...)
			got := runDebugCaptureCommand(t, server, append([]string{"--debug", "--debug-body"}, args...)...)
			if control.code != 0 || got.code != control.code || got.stdout != control.stdout || !strings.HasSuffix(got.stderr, control.stderr) {
				t.Fatalf("body capture changed output: control=%+v capture=%+v", control, got)
			}
			if len(hashes) != 2 || <-hashes != <-hashes {
				t.Fatal("body capture changed request bytes or request count")
			}
			for _, direction := range []string{"request", "response"} {
				pattern := regexp.MustCompile(`HTTP attempt 1: ` + direction + ` body \(redacted; [0-9]+ bytes observed; complete read\):`)
				if len(pattern.FindAllString(got.stderr, -1)) != 1 {
					t.Errorf("missing complete %s preview: %q", direction, got.stderr)
				}
			}
			for _, view := range []string{`"input":"<REDACTED>"`, `"total_tokens":3`} {
				if !strings.Contains(got.stderr, view) {
					t.Errorf("missing useful redacted structure %q: %q", view, got.stderr)
				}
			}
			assertDebugCapturePrivate(t, got.stderr, "synthetic-private-prompt", "synthetic-private-answer", "synthetic-private-key", "synthetic-private-value", "synthetic-private-header")
		})
	}
}

func TestMainDebugCaptureRequiresDebugAndValidLimits(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"model_synthetic","object":"model"}`)
	}))
	t.Cleanup(server.Close)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--debug-body"}, "--debug-body requires --debug"},
		{[]string{"--debug-sse"}, "--debug-sse requires --debug"},
		{[]string{"--debug-event-timing"}, "--debug-event-timing requires --debug"},
		{[]string{"--debug-max-bytes", "16384"}, "--debug-max-bytes requires --debug"},
		{[]string{"--debug-max-events", "64"}, "--debug-max-events requires --debug"},
		{[]string{"--debug=false", "--debug-sse"}, "--debug-sse requires --debug"},
		{[]string{"--debug-body=false", "--debug-max-bytes", "16384"}, "--debug-max-bytes requires --debug"},
		{[]string{"--debug-event-timing=false", "--debug-max-events", "64"}, "--debug-max-events requires --debug"},
		{[]string{"--debug", "--debug-max-bytes", "0"}, "--debug-max-bytes must be between 1 and 1048576"},
		{[]string{"--debug", "--debug-max-bytes", "1048577"}, "--debug-max-bytes must be between 1 and 1048576"},
		{[]string{"--debug", "--debug-max-events", "0"}, "--debug-max-events must be between 1 and 4096"},
		{[]string{"--debug", "--debug-max-events", "4097"}, "--debug-max-events must be between 1 and 4096"},
		{[]string{"--debug", "--debug-max-bytes", "synthetic-private-limit"}, "Invalid value for --debug-max-bytes."},
	} {
		for _, format := range []string{"text", "json", "jsonl"} {
			t.Run(format+"/"+strings.Join(tc.args, " "), func(t *testing.T) {
				args := []string{"--format-error", format, "models", "retrieve", "model_synthetic"}
				if format == "jsonl" {
					args = append(args, "--quiet")
				}
				got := runDebugCaptureCommand(t, server, append(args, tc.args...)...)
				if got.code != 1 || got.stdout != "" {
					t.Fatalf("invalid capture configuration changed exit or stdout: %+v", got)
				}
				message := got.stderr
				if format != "text" {
					var diagnostic struct {
						Message string `json:"message"`
					}
					if err := json.Unmarshal([]byte(got.stderr), &diagnostic); err != nil {
						t.Fatalf("invalid configuration corrupted structured stderr: %v; %q", err, got.stderr)
					}
					message = diagnostic.Message
				}
				if !strings.Contains(message, tc.want) {
					t.Fatalf("invalid capture configuration omitted safe guidance %q: %+v", tc.want, got)
				}
				assertDebugCapturePrivate(t, got.stderr, "synthetic-private-limit")
			})
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid debug options sent %d requests", requests.Load())
	}
}

func TestMainDebugCaptureModesRemainIndependent(t *testing.T) {
	server := streamingTextServer(t, []string{
		`{"type":"response.output_text.delta","delta":"synthetic-private-event","sequence_number":0}`,
		`{"type":"response.completed","response":{"status":"completed","output":[]}}`,
	})
	args := streamingTextArgs("responses", "--format", "jsonl")
	control := runDebugCaptureCommand(t, server, args...)
	if control.code != 0 || control.stderr != "" {
		t.Fatalf("stream control failed: %+v", control)
	}
	for _, tc := range []struct {
		flags                []string
		body, frames, events bool
	}{
		{nil, false, false, false},
		{[]string{"--debug-body=false", "--debug-sse=false", "--debug-event-timing=false"}, false, false, false},
		{[]string{"--debug-max-bytes", "1024", "--debug-max-events", "2"}, false, false, false},
		{[]string{"--debug-body"}, true, false, false},
		{[]string{"--debug-sse"}, false, true, false},
		{[]string{"--debug-event-timing"}, false, false, true},
		{[]string{"--debug-body", "--debug-sse", "--debug-event-timing", "--quiet", "--format-error", "json"}, true, true, true},
	} {
		t.Run(strings.Join(tc.flags, " "), func(t *testing.T) {
			flags := append([]string{"--debug"}, tc.flags...)
			got := runDebugCaptureCommand(t, server, append(flags, args...)...)
			if got.code != control.code || got.stdout != control.stdout {
				t.Fatalf("capture mode changed streaming output: control=%+v capture=%+v", control, got)
			}
			for _, category := range []struct {
				marker string
				want   bool
			}{{"request body (", tc.body}, {"response body (", tc.body}, {"SSE frame ", tc.frames}, {"SSE event ", tc.events}} {
				if strings.Contains(got.stderr, category.marker) != category.want {
					t.Errorf("category %q enabled=%t: %q", category.marker, category.want, got.stderr)
				}
			}
			assertDebugCapturePrivate(t, got.stderr, "synthetic input", "synthetic-private-event")
		})
	}
}

func TestMainDebugCaptureFalseFlagsKeepOrdinaryOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"model_synthetic","object":"model"}`)
	}))
	t.Cleanup(server.Close)
	args := []string{"--format", "json", "models", "retrieve", "model_synthetic"}
	control := runDebugCaptureCommand(t, server, args...)
	if control.code != 0 || control.stderr != "" {
		t.Fatalf("ordinary request failed: %+v", control)
	}
	for _, flags := range [][]string{
		{"--debug-body=false", "--debug-sse=false", "--debug-event-timing=false"},
		{"--debug=false", "--debug-body=false", "--debug-sse=false", "--debug-event-timing=false"},
	} {
		if got := runDebugCaptureCommand(t, server, append(flags, args...)...); got != control {
			t.Fatalf("disabled capture changed ordinary output: flags=%v got=%+v control=%+v", flags, got, control)
		}
	}
}

func TestMainDebugCaptureStreamsPreserveEveryFormat(t *testing.T) {
	server := streamingTextServer(t, []string{
		`{"type":"response.output_text.delta","delta":"synthetic-private-event","sequence_number":0}`,
		`{"type":"future.event","synthetic-private-key":"synthetic-private-value"}`,
		`{"type":"response.completed","response":{"status":"completed","output":[]}}`,
	})
	for _, format := range []string{"auto", "text", "json", "jsonl", "raw", "yaml", "pretty", "explore"} {
		t.Run(format, func(t *testing.T) {
			args := streamingTextArgs("responses", "--format", format)
			control := runDebugCaptureCommand(t, server, args...)
			flags := []string{"--debug", "--debug-body", "--debug-sse", "--debug-event-timing"}
			got := runDebugCaptureCommand(t, server, append(flags, args...)...)
			if control.code != 0 || got.code != control.code || got.stdout != control.stdout {
				t.Fatalf("capture changed %s stream output: control=%+v capture=%+v", format, control, got)
			}
			const warning = "Warning: Output format 'explore' not supported for non-terminal output; falling back to 'json'\n"
			wantWarnings := 0
			if format == "explore" {
				wantWarnings = 1
			}
			if control.stderr != strings.Repeat(warning, wantWarnings) || strings.Count(got.stderr, warning) != wantWarnings || strings.Count(got.stderr, "Warning:") != wantWarnings {
				t.Fatalf("capture changed the existing stream warning: control=%+v capture=%+v", control, got)
			}
			// The fixture also sends [DONE], which is a complete observed data frame.
			if strings.Count(got.stderr, "complete frame observed after") != 4 {
				t.Fatalf("capture lost stream frame timing: %q", got.stderr)
			}
			assertDebugCapturePrivate(t, got.stderr, "synthetic input", "synthetic-private-event", "synthetic-private-key", "synthetic-private-value")
		})
	}
}

func TestMainDebugCaptureStreamFailuresKeepPartialOutput(t *testing.T) {
	for _, last := range []string{
		`{"type":"response.failed","response":{"status":"failed","error":{"message":"synthetic API failure","code":"server_error"}}}`,
		`{"error":{"message":"synthetic API error","type":"server_error"}}`,
		`{"type":"response.output_text.delta","delta":"synthetic-private-malformed`,
	} {
		for _, format := range []string{"text", "jsonl"} {
			t.Run(fmt.Sprintf("%s/%d", format, len(last)), func(t *testing.T) {
				server := streamingTextServer(t, []string{`{"type":"response.output_text.delta","delta":"synthetic-private-partial","sequence_number":0}`, last})
				args := streamingTextArgs("responses", "--format", format, "--format-error", "json")
				control := runDebugCaptureCommand(t, server, args...)
				flags := []string{"--debug", "--debug-body", "--debug-sse", "--debug-event-timing"}
				got := runDebugCaptureCommand(t, server, append(flags, args...)...)
				if control.code == 0 || got.code != control.code || got.stdout != control.stdout || !strings.HasSuffix(got.stderr, control.stderr) {
					t.Fatalf("capture changed stream failure: control=%+v capture=%+v", control, got)
				}
				if !strings.Contains(got.stdout, "synthetic-private-partial") {
					t.Fatal("stream failure lost prior output")
				}
				assertDebugCapturePrivate(t, strings.TrimSuffix(got.stderr, control.stderr), "synthetic input", "synthetic-private-partial", "synthetic-private-malformed")
			})
		}
	}
}

func TestMainDebugCaptureRetriesAndErrorStatus(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprintf("failure=%t", failure), func(t *testing.T) {
			var requests atomic.Int32
			hashes := make(chan [32]byte, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				select {
				case hashes <- sha256.Sum256(body):
				default:
					t.Error("unexpected extra request")
				}
				attempt := requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if failure {
					w.WriteHeader(http.StatusBadRequest)
					io.WriteString(w, `{"error":{"message":"synthetic error","type":"invalid_request_error","token":"synthetic-private-token"}}`)
				} else if attempt%2 == 1 {
					w.Header().Set("Retry-After-Ms", "1")
					w.WriteHeader(http.StatusServiceUnavailable)
					io.WriteString(w, `{"error":{"message":"synthetic-private-retry","type":"server_error"}}`)
				} else {
					io.WriteString(w, `{"id":"resp_synthetic","object":"response","status":"completed","output":[]}`)
				}
			}))
			t.Cleanup(server.Close)
			args := []string{"--format", "json", "--format-error", "json", "responses", "create", "--model", "fake-model", "--input", "synthetic-private-prompt"}
			control := runDebugCaptureCommand(t, server, args...)
			got := runDebugCaptureCommand(t, server, append([]string{"--debug", "--debug-body", "--quiet"}, args...)...)
			if (control.code != 0) != failure || got.code != control.code || got.stdout != control.stdout || !strings.HasSuffix(got.stderr, control.stderr) {
				t.Fatalf("capture changed retry/error behavior: control=%+v capture=%+v", control, got)
			}
			wantRequests := 4
			if failure {
				wantRequests = 2
			}
			if int(requests.Load()) != wantRequests || len(hashes) != wantRequests {
				t.Fatalf("request count=%d, want %d", requests.Load(), wantRequests)
			}
			first := <-hashes
			for len(hashes) > 0 {
				if <-hashes != first {
					t.Fatal("retry or body capture changed request bytes")
				}
			}
			diagnostics := strings.TrimSuffix(got.stderr, control.stderr)
			if failure {
				assertDebugTimingStage(t, diagnostics, 1, "response headers received", "400 Bad Request")
			} else {
				assertDebugTimingStage(t, diagnostics, 1, "response headers received", "503 Service Unavailable")
				assertDebugTimingStage(t, diagnostics, 2, "response headers received", "200 OK")
			}
			if !strings.Contains(diagnostics, "request body (") || !strings.Contains(diagnostics, "response body (") {
				t.Fatalf("body capture omitted the retry/error lifecycle: %q", diagnostics)
			}
			assertDebugCapturePrivate(t, diagnostics, "synthetic-private-prompt", "synthetic-private-token", "synthetic-private-retry")
		})
	}
}

func TestMainDebugCaptureExhaustedAndMalformedErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body    string
		status, calls int
	}{
		{"retries exhausted", `{"error":{"message":"synthetic unavailable","type":"server_error"}}`, http.StatusServiceUnavailable, 3},
		{"empty 400", "", http.StatusBadRequest, 1},
		{"malformed 400", `{"error":"synthetic-private-incomplete`, http.StatusBadRequest, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After-Ms", "1")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			t.Cleanup(server.Close)
			args := []string{"--format-error", "json", "models", "retrieve", "model_synthetic"}
			control := runDebugCaptureCommand(t, server, args...)
			got := runDebugCaptureCommand(t, server, append([]string{"--debug", "--debug-body"}, args...)...)
			if control.code == 0 || got.code != control.code || got.stdout != control.stdout || !strings.HasSuffix(got.stderr, control.stderr) || int(requests.Load()) != 2*tc.calls {
				t.Fatalf("capture changed API failure: control=%+v capture=%+v requests=%d", control, got, requests.Load())
			}
			diagnostics := strings.TrimSuffix(got.stderr, control.stderr)
			if strings.Count(diagnostics, "response headers received after") != tc.calls || !strings.Contains(diagnostics, "response body (redacted;") {
				t.Fatalf("capture lost attempts or error body observation: %q", diagnostics)
			}
			assertDebugCapturePrivate(t, diagnostics, "synthetic-private-incomplete")
		})
	}
}

func TestMainDebugCaptureStreamLimitsKeepOwnership(t *testing.T) {
	for _, limit := range []string{"0", "1", "-1"} {
		t.Run(limit, func(t *testing.T) {
			closed := make(chan struct{}, 2)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				defer func() {
					select {
					case closed <- struct{}{}:
					default:
						t.Error("unexpected extra stream completion")
					}
				}()
				w.Header().Set("Content-Type", "text/event-stream")
				writeStreamingTextEvent(w, `{"type":"response.output_text.delta","delta":"synthetic-private-event","sequence_number":0}`)
				w.(http.Flusher).Flush()
				if limit != "-1" {
					<-r.Context().Done()
					return
				}
				writeStreamingTextEvent(w, `{"type":"response.completed","response":{"status":"completed","output":[]}}`)
			}))
			t.Cleanup(server.Close)
			t.Cleanup(server.CloseClientConnections)
			args := append(streamingTextArgs("responses", "--format", "jsonl"), "--max-items", limit)
			control := runDebugCaptureCommand(t, server, args...)
			flags := []string{"--debug", "--debug-body", "--debug-sse", "--debug-event-timing"}
			got := runDebugCaptureCommand(t, server, append(flags, args...)...)
			if control.code != 0 || control.stderr != "" || got.code != control.code || got.stdout != control.stdout {
				t.Fatalf("capture changed stream limit %s: control=%+v capture=%+v", limit, control, got)
			}
			for range 2 {
				select {
				case <-closed:
				case <-time.After(3 * time.Second):
					t.Fatal("limited stream retained its response")
				}
			}
			if requests.Load() != 2 {
				t.Fatalf("stream limit changed request count: %d", requests.Load())
			}
			if limit == "0" && (strings.Contains(got.stderr, "first response data read") || strings.Contains(got.stderr, "SSE frame ")) {
				t.Fatalf("capture read ahead of the zero-item consumer: %q", got.stderr)
			}
			if limit == "-1" {
				assertDebugTimingStage(t, got.stderr, 1, "response body fully consumed", "")
			} else {
				assertDebugTimingStage(t, got.stderr, 1, "response body closed before EOF", "")
			}
			assertDebugCapturePrivate(t, got.stderr, "synthetic-private-event", "synthetic input")
		})
	}
}

func TestMainDebugCapturePublicHelp(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"responses", "create", "--help"}} {
		got := runMainDispatch(t, "bash", append([]string{"openai"}, args...)...)
		if got.code != 0 || got.stderr != "" {
			t.Fatalf("debug help failed: %+v", got)
		}
		for _, name := range []string{"--debug-body", "--debug-sse", "--debug-event-timing", "--debug-max-bytes", "--debug-max-events"} {
			if !strings.Contains(got.stdout, name) {
				t.Errorf("help omitted %s", name)
			}
		}
	}
}

func TestMainDebugCaptureSSEFrameHandshake(t *testing.T) {
	const first = `{"type":"response.output_text.delta","delta":"synthetic-private-first","sequence_number":0}`
	const frame = "event: response.output_text.delta\r\ndata: " + first + "\r\n\r\n"
	for _, tc := range []struct {
		name, prefix string
		flags        []string
	}{
		{"jsonl", first + "\n", []string{"--format", "jsonl"}},
		{"raw text", "synthetic-private-first\n", []string{"--format", "text", "--transform", "delta", "--raw-output"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			continuation, final := make(chan struct{}), make(chan struct{})
			var continuationOnce, finalOnce sync.Once
			releaseContinuation := func() { continuationOnce.Do(func() { close(continuation) }) }
			releaseFinal := func() { finalOnce.Do(func() { close(final) }) }
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
				io.WriteString(w, ": synthetic-private-comment\r\n\r\n"+frame[:len(frame)/2])
				w.(http.Flusher).Flush()
				select {
				case <-continuation:
				case <-r.Context().Done():
					return
				}
				io.WriteString(w, frame[len(frame)/2:])
				w.(http.Flusher).Flush()
				select {
				case <-final:
				case <-r.Context().Done():
					return
				}
				writeStreamingTextEvent(w, `{"type":"future.event","synthetic-private-key":"synthetic-private-value"}`)
				writeStreamingTextEvent(w, `{"type":"response.completed","response":{"status":"completed","output":[]}}`)
			}))
			t.Cleanup(server.Close)
			t.Cleanup(releaseFinal)
			t.Cleanup(releaseContinuation)
			flags := append([]string{"--debug", "--debug-body", "--debug-sse", "--debug-event-timing"}, tc.flags...)
			child, stdout, stderr, ctx := startDebugTimingProcess(t, server, streamingTextArgs("responses", flags...)...)
			stderr.waitFor(t, ctx, "SSE frame 1 (")
			assertDebugTimingStage(t, stderr.String(), 1, "first response data read", "")
			if strings.Contains(stderr.String(), "SSE event ") || strings.Contains(stderr.String(), "response body fully consumed") {
				t.Fatalf("comment/partial frame was labeled a complete event: %q", stderr.String())
			}
			releaseContinuation()
			readStreamingTextPrefix(t, ctx, stdout, tc.prefix)
			stderr.waitFor(t, ctx, "SSE event 1 complete frame observed after")
			if strings.Contains(stderr.String(), "response body fully consumed") {
				t.Fatalf("body finished before the withheld final event: %q", stderr.String())
			}
			releaseFinal()
			rest, err := io.ReadAll(stdout)
			if err != nil {
				t.Fatal(err)
			}
			if err := child.Wait(); err != nil {
				t.Fatalf("captured stream failed: %v; stderr=%q", err, stderr.String())
			}
			log := stderr.String()
			if strings.Count(log, "complete frame observed after") != 3 || !strings.Contains(log, `\r\n`) {
				t.Fatalf("capture lost unknown events or escaped wire framing: %q", log)
			}
			assertDebugTimingStage(t, log, 1, "response body fully consumed", "")
			assertDebugCapturePrivate(t, log, "synthetic input", "synthetic-private-first", "synthetic-private-comment", "synthetic-private-key", "synthetic-private-value")
			control := runDebugCaptureCommand(t, server, streamingTextArgs("responses", tc.flags...)...)
			if control.code != 0 || control.stderr != "" || control.stdout != tc.prefix+string(rest) {
				t.Fatalf("captured stream changed output: stdout=%q control=%+v", tc.prefix+string(rest), control)
			}
		})
	}
}

func TestMainDebugCaptureFrameLimitsKeepAllOutput(t *testing.T) {
	server := streamingTextServer(t, []string{
		`{"type":"response.output_text.delta","delta":"synthetic-first","sequence_number":0}`,
		`{"type":"response.output_text.delta","delta":"synthetic-second","sequence_number":1}`,
		`{"type":"response.completed","response":{"status":"completed","output":[]}}`,
	})
	args := streamingTextArgs("responses", "--format", "jsonl")
	control := runDebugCaptureCommand(t, server, args...)
	got := runDebugCaptureCommand(t, server, append([]string{"--debug", "--debug-sse", "--debug-event-timing", "--debug-max-events", "1", "--debug-max-bytes", "128"}, args...)...)
	if control.code != 0 || got.code != control.code || got.stdout != control.stdout {
		t.Fatalf("diagnostic limits changed stream data: control=%+v capture=%+v", control, got)
	}
	if strings.Count(got.stderr, "complete frame observed after") != 1 || strings.Contains(got.stderr, "SSE frame 2 (") || !strings.Contains(got.stderr, "SSE diagnostics truncated by capture limits") {
		t.Fatalf("diagnostic limits were not enforced or disclosed: %q", got.stderr)
	}
	assertDebugCapturePrivate(t, got.stderr, "synthetic-first", "synthetic-second", "synthetic input")
}

func TestMainDebugCaptureLargePayloads(t *testing.T) {
	testMainDebugLargePayloads(t, []string{"--debug-body", "--debug-sse", "--debug-event-timing", "--debug-max-bytes", "1024", "--debug-max-events", "2"})
}

func runDebugCaptureCommand(t *testing.T, server *httptest.Server, args ...string) mainDispatchResult {
	t.Helper()
	return runMainDispatchWithEnv(t, "bash", debugCaptureProcessEnv(t, server), append([]string{"openai"}, args...)...)
}

func debugCaptureProcessEnv(t *testing.T, server *httptest.Server) []string {
	t.Helper()
	home := t.TempDir()
	return []string{
		"OPENAI_API_KEY=sk-fake-debug-capture", "OPENAI_BASE_URL=" + server.URL, "FORCE_COLOR=0",
		"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home,
		"APPDATA=" + home, "LOCALAPPDATA=" + home, "GOMAXPROCS=2",
	}
}

func assertDebugCapturePrivate(t *testing.T, log string, private ...string) {
	t.Helper()
	for _, value := range append(private, "sk-fake-debug-capture", "sk-fake-debug-timing", "\x1b", "\u202e") {
		if strings.Contains(log, value) {
			t.Errorf("debug capture disclosed private or terminal-control content %q", value)
		}
	}
}
