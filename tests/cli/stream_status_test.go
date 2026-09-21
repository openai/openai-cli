package cli_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMainAssistantStreamFailuresReturnNonzero(t *testing.T) {
	for _, status := range []string{"failed", "cancelled", "expired", "incomplete"} {
		t.Run(status, func(t *testing.T) {
			body := fmt.Sprintf(`{"id":"run_synthetic","object":"thread.run","status":%q,"thread_id":"thread_synthetic","assistant_id":"asst_synthetic","last_error":{"code":"server_error","message":"Synthetic failure detail"}}`, status)
			wire := "event: thread.run." + status + "\ndata: " + body + "\n\ndata: [DONE]\n\n"
			server := readableTestServer(t, "POST /threads/thread_synthetic/runs", "text/event-stream", wire, http.StatusOK)
			for _, format := range []string{"auto", "json", "raw", "yaml"} {
				args := []string{"--format", format, "beta:threads:runs", "create", "--thread-id", "thread_synthetic", "--assistant-id", "asst_synthetic", "--stream", "true"}
				got := runReadableMain(t, server.URL, nil, args...)
				if got.code == 0 || !strings.Contains(got.stderr, "streamed") || !strings.Contains(got.stdout, "run_synthetic") {
					t.Fatalf("%s/%s must preserve event and report failure: %+v", status, format, got)
				}
				if format == "json" || format == "raw" {
					assertReadableJSONValues(t, got.stdout, `{"event":"thread.run.`+status+`","data":`+body+`}`)
				}
			}
		})
	}
}

func TestMainSpeechStreamFailurePreservesOutput(t *testing.T) {
	for _, test := range []struct{ name, header, event string }{
		{"typed event", "", `{"type":"error","code":"server_error","message":"Synthetic failure"}`},
		{"named SSE event", "event: error\r\n", `{"code":"server_error","message":"Synthetic failure"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			wire := ": heartbeat\r\n" + test.header + "data: " + test.event + "\r\n\r\ndata: [DONE]\r\n\r\n: trailing bytes\r\n"
			server := readableTestServer(t, "POST /audio/speech", "text/event-stream", wire, http.StatusOK)
			for _, flags := range [][]string{nil, {"--format", "json"}, {"--format", "jsonl"}, {"--format", "yaml"}, {"--format", "raw"}, {"--raw-output"}, {"--transform", "code"}, {"--raw-output", "--transform", "code"}} {
				args := append(append([]string{}, flags...), "audio:speech", "create", "--model", "model-synthetic", "--voice", "alloy", "--input", "Synthetic text", "--stream-format", "sse")
				got := runReadableMain(t, server.URL, nil, args...)
				if got.code == 0 || !strings.Contains(got.stderr, "streaming") {
					t.Fatalf("speech API error was not reported with %v: %+v", flags, got)
				}
				switch strings.Join(flags, " ") {
				case "--format raw", "--raw-output":
					if got.stdout != wire {
						t.Fatalf("raw speech error changed original SSE bytes: %q", got.stdout)
					}
				case "--format json", "--format jsonl":
					assertReadableJSONValues(t, got.stdout, test.event)
				case "--transform code":
					assertReadableJSONValues(t, got.stdout, `"server_error"`)
				case "--raw-output --transform code":
					if got.stdout != "server_error\n" {
						t.Fatalf("error status changed extracted output: %q", got.stdout)
					}
				default:
					if !strings.Contains(got.stdout, "Synthetic failure") {
						t.Fatalf("speech failure lost the error event: %+v", got)
					}
				}
			}
		})
	}
}

func TestMainSpeechExplicitDestinationReportsFailure(t *testing.T) {
	for _, header := range []string{"", "event: error\r\n"} {
		t.Run(header, func(t *testing.T) {
			event := `{"type":"error","code":"server_error","message":"Synthetic failure"}`
			if header != "" {
				event = `{"code":"server_error","message":"Synthetic failure"}`
			}
			wire := ": heartbeat\r\n" + header + "data: " + event + "\r\n\r\ndata: [DONE]\r\n\r\n: trailing bytes\r\n"
			server := readableTestServer(t, "POST /audio/speech", "text/event-stream", wire, http.StatusOK)
			for _, destination := range []string{"-", "/dev/stdout", filepath.Join(t.TempDir(), "failed-speech.sse")} {
				args := []string{"--format", "json", "audio:speech", "create", "--model", "model-synthetic", "--voice", "alloy", "--input", "Synthetic text", "--stream-format", "sse", "--output", destination}
				got := runReadableMain(t, server.URL, nil, args...)
				if got.code == 0 || !strings.Contains(got.stderr, "streaming") {
					t.Fatalf("explicit destination hid speech failure: destination=%q result=%+v", destination, got)
				}
				if destination == "-" || destination == "/dev/stdout" {
					if got.stdout != wire {
						t.Fatalf("explicit stdout changed speech bytes: %q", got.stdout)
					}
				} else {
					content, err := os.ReadFile(destination)
					if err != nil || string(content) != wire {
						t.Fatalf("explicit speech file lost failure bytes: %v; %q", err, content)
					}
					if got.stdout != "" {
						t.Fatalf("failed speech download printed a success message: %q", got.stdout)
					}
				}
			}
		})
	}
}
