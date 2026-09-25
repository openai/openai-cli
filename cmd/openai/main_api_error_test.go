package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/goccy/go-yaml"
)

func TestMainAPIErrorsWithoutJSONDetails(t *testing.T) {
	for _, test := range []struct {
		name, contentType, body string
		status                  int
	}{
		{"HTML gateway error", "text/html", "<html>synthetic private proxy detail</html>", http.StatusBadGateway},
		{"text gateway error", "text/plain", "synthetic private proxy detail", http.StatusBadGateway},
		{"malformed JSON", "application/json", `{"error":{"message":"synthetic private proxy detail"`, http.StatusBadGateway},
		{"empty rate limit", "application/json", "", http.StatusTooManyRequests},
		{"null error details", "application/json", `{"error":null}`, http.StatusBadGateway},
	} {
		for _, flag := range []string{"--format-error", "--format"} {
			for _, format := range []string{"json", "jsonl", "raw", "yaml"} {
				t.Run(test.name+"/"+flag+"/"+format, func(t *testing.T) {
					result := runMainAPIErrorResponse(t, test.status, test.contentType, test.body, flag, format)
					payload := decodeMainStructuredError(t, format, result.stderr)
					wantMessage := fmt.Sprintf("HTTP %d %s: the server returned no usable JSON error details.", test.status, http.StatusText(test.status))
					if payload["status_code"] != json.Number(fmt.Sprint(test.status)) || payload["message"] != wantMessage {
						t.Errorf("error = %#v, want status=%d message=%q", payload, test.status, wantMessage)
					}
					if strings.Contains(result.stderr, "synthetic private proxy detail") {
						t.Error("fallback included the unstructured response body")
					}
				})
			}
		}
	}
}

func TestMainAPIErrorsPreserveJSONDetailsAndExtraction(t *testing.T) {
	const details = `{"message":"synthetic error","type":"invalid_request_error","code":"synthetic_code","param":null,"future_field":{"sequence":9007199254740993}}`
	want := decodeMainErrorObject(t, "json", details)
	for _, flag := range []string{"--format-error", "--format"} {
		for _, format := range []string{"json", "jsonl", "raw", "yaml"} {
			t.Run(flag+"/"+format, func(t *testing.T) {
				result := runMainAPIErrorResponse(t, http.StatusBadRequest, "application/json", `{"error":`+details+`}`, flag, format)
				if got := decodeMainErrorObject(t, format, result.stderr); !reflect.DeepEqual(got, want) {
					t.Errorf("API error fields changed: got %#v; want %#v", got, want)
				}
			})
		}
	}
	for _, flags := range [][]string{{"--format-error", "json"}, {"--format", "json"}, nil} {
		t.Run("extraction/"+strings.Join(flags, "/"), func(t *testing.T) {
			flags = append(flags, "--transform-error", "future_field.sequence")
			result := runMainAPIErrorResponse(t, http.StatusBadRequest, "application/json", `{"error":`+details+`}`, flags...)
			if strings.TrimSpace(result.stderr) != "9007199254740993" {
				t.Errorf("error extraction changed: %q", result.stderr)
			}
		})
	}
}

// SDK decoding failures may not retain the HTTP response. They still need a
// structured diagnostic, but must not be assigned an invented HTTP status.
func TestMainAPIErrorsWithUnexpectedJSONShapes(t *testing.T) {
	for _, details := range []string{
		`"synthetic private response detail"`, `42`, `true`, `[]`,
		`{"message":42}`, `{"type":false}`, `{"code":[]}`, `{"param":{}}`,
	} {
		for _, flag := range []string{"--format-error", "--format"} {
			for _, format := range []string{"json", "jsonl", "raw", "yaml"} {
				t.Run(details+"/"+flag+"/"+format, func(t *testing.T) {
					result := runMainAPIErrorResponse(t, http.StatusBadGateway, "application/json", `{"error":`+details+`}`, flag, format)
					var payload map[string]any
					if strings.HasPrefix(details, "{") {
						// Known fields with unexpected types remain available as
						// original JSON when the SDK retains the error object.
						payload = decodeMainErrorObject(t, format, result.stderr)
						if want := decodeMainErrorObject(t, "json", details); !reflect.DeepEqual(payload, want) {
							t.Errorf("original API fields changed: got %#v, want %#v", payload, want)
						}
					} else {
						payload = decodeMainStructuredError(t, format, result.stderr)
					}
					if _, exists := payload["status_code"]; exists {
						t.Errorf("invented an HTTP status: %#v", payload)
					}
					if strings.Contains(result.stderr, "synthetic private response detail") {
						t.Errorf("diagnostic contains private response data: %q", result.stderr)
					}
				})
			}
		}
	}
}

func TestMainAPIErrorsStructuredAcrossCommands(t *testing.T) {
	for _, command := range [][]string{
		{"models", "list"},
		{"responses", "create", "--model", "synthetic-model", "--input", "synthetic private input"},
	} {
		for _, format := range []string{"json", "jsonl", "raw", "yaml"} {
			t.Run(strings.Join(command[:2], "/")+"/"+format, func(t *testing.T) {
				result := runMainCommandAPIErrorResponse(t, http.StatusBadGateway, "application/json", `{"error":true}`, command, "--format", format)
				decodeMainStructuredError(t, format, result.stderr)
				if strings.Contains(result.stderr, "synthetic private input") {
					t.Error("diagnostic included the request body")
				}
			})
		}
	}
}

func TestMainAPIErrorsFormatPrecedenceAndExtraction(t *testing.T) {
	for _, body := range []string{`{"error":true}`, `{"error":{"message":"synthetic private response detail"}}`} {
		for _, flags := range [][]string{
			{"--format", "text", "--format-error", "json"},
			{"--format", "yaml", "--format-error", "json"},
			{"--format-error", "JSON"},
			{"--format", "JSON"},
		} {
			t.Run(body+"/"+strings.Join(flags, "/"), func(t *testing.T) {
				result := runMainAPIErrorResponse(t, http.StatusBadGateway, "application/json", body, flags...)
				decodeMainStructuredError(t, "json", result.stderr)
			})
		}
		for _, format := range []string{"auto", "text"} {
			t.Run(body+"/readable override/"+format, func(t *testing.T) {
				result := runMainAPIErrorResponse(t, http.StatusBadGateway, "application/json", body, "--format", "json", "--format-error", format)
				if strings.TrimSpace(result.stderr) == "" || json.Valid([]byte(result.stderr)) {
					t.Errorf("expected a readable error, got %q", result.stderr)
				}
				if strings.Contains(result.stderr, "synthetic private response detail") {
					t.Error("readable error included the response body")
				}
			})
		}
	}
	result := runMainAPIErrorResponse(t, http.StatusBadGateway, "application/json", `{"error":true}`, "--format-error", "json", "--transform-error", "message")
	var message string
	if err := json.Unmarshal([]byte(result.stderr), &message); err != nil || strings.TrimSpace(message) == "" {
		t.Errorf("expected JSON message extraction, got %q (%v)", result.stderr, err)
	}
}

func decodeMainStructuredError(t *testing.T, format, output string) map[string]any {
	t.Helper()
	payload := decodeMainErrorObject(t, format, output)
	if message, ok := payload["message"].(string); !ok || strings.TrimSpace(message) == "" {
		t.Errorf("expected a nonempty diagnostic message: %q", output)
	}
	return payload
}

func decodeMainErrorObject(t *testing.T, format, output string) map[string]any {
	t.Helper()
	data := []byte(output)
	if format == "yaml" {
		var err error
		data, err = yaml.YAMLToJSON(data)
		if err != nil {
			t.Fatalf("stderr is not YAML: %v; %q", err, output)
		}
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		t.Fatalf("stderr is not a %s error object: %v; %q", format, err, output)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatalf("unexpected trailing data in %s error: %v; %q", format, err, output)
	}
	if format == "jsonl" && strings.Count(output, "\n") != 1 {
		t.Errorf("JSONL error spans multiple lines: %q", output)
	}
	return payload
}

func runMainAPIErrorResponse(t *testing.T, status int, contentType, body string, flags ...string) mainDispatchResult {
	t.Helper()
	return runMainCommandAPIErrorResponse(t, status, contentType, body, []string{"models", "retrieve", "--model", "synthetic-model"}, flags...)
}

func runMainCommandAPIErrorResponse(t *testing.T, status int, contentType, body string, command []string, flags ...string) mainDispatchResult {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("x-should-retry", "false")
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	args := append([]string{"./openai", "--base-url", server.URL}, flags...)
	args = append(args, command...)
	result := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_API_KEY=synthetic-helpful-errors-key"}, args...)
	if result.code != 1 || requests.Load() != 1 || result.stdout != "" {
		t.Fatalf("exit=%d requests=%d stdout=%q, want exit=1 requests=1 empty stdout; stderr=%q", result.code, requests.Load(), result.stdout, result.stderr)
	}
	if strings.Contains(result.stderr, "synthetic-helpful-errors-key") {
		t.Error("error presentation exposed the API key")
	}
	return result
}

func TestMainStreamErrorsPreserveEventDetails(t *testing.T) {
	const event = `{"error":{"message":"synthetic private stream detail","code":"server_error","future_field":{"sequence":9007199254740993}},"event_extension":"preserved"}`
	for _, partial := range []bool{false, true} {
		for _, test := range []struct {
			name, format, extracted string
			flags                   []string
		}{
			{"default", "text", "", nil},
			{"explicit JSON", "json", "", []string{"--format-error", "json"}},
			{"inherited JSON", "json", "", []string{"--format", "json"}},
			{"JSONL", "jsonl", "", []string{"--format-error", "jsonl"}},
			{"raw", "raw", "", []string{"--format-error", "raw"}},
			{"YAML", "yaml", "", []string{"--format-error", "yaml"}},
			{"text override", "text", "", []string{"--format", "json", "--format-error", "auto"}},
			{"error extraction", "json", "9007199254740993", []string{"--transform-error", "error.future_field.sequence"}},
			{"independent extraction", "json", `"server_error"`, []string{"--transform", "delta", "--raw-output", "--format-error", "json", "--transform-error", "error.code"}},
		} {
			t.Run(fmt.Sprintf("partial=%t/%s", partial, test.name), func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					if partial {
						fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"synthetic first chunk\",\"sequence_number\":1}\n\n")
						w.(http.Flusher).Flush()
					}
					fmt.Fprintf(w, "event: error\ndata: %s\n\n", event)
				}))
				t.Cleanup(server.Close)
				args := append([]string{"openai", "--base-url", server.URL}, test.flags...)
				args = append(args, "responses", "create", "--model", "synthetic-model", "--input", "synthetic input", "--stream", "true")
				result := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_API_KEY=synthetic-stream-key"}, args...)
				if result.code != 1 || requests.Load() != 1 {
					t.Fatalf("exit=%d requests=%d, want 1 each; stderr=%q", result.code, requests.Load(), result.stderr)
				}
				if partial != strings.Contains(result.stdout, "synthetic first chunk") || strings.Contains(result.stdout, "synthetic private stream detail") {
					t.Errorf("partial stream output changed: %q", result.stdout)
				}
				if !partial && result.stdout != "" {
					t.Errorf("stream with no successful events wrote stdout: %q", result.stdout)
				}
				if strings.Contains(result.stderr, "synthetic-stream-key") {
					t.Error("error included the request key")
				}
				switch {
				case test.format == "text":
					if !strings.Contains(result.stderr, "The response stream failed. Output may be incomplete.") || strings.Contains(result.stderr, "synthetic private stream detail") || strings.Contains(result.stderr, "200") {
						t.Errorf("missing safe stream failure summary: %q", result.stderr)
					}
				case test.extracted != "":
					if strings.TrimSpace(result.stderr) != test.extracted {
						t.Errorf("extracted=%q, want %q", result.stderr, test.extracted)
					}
				default:
					if got, want := decodeMainErrorObject(t, test.format, result.stderr), decodeMainErrorObject(t, "json", event); !reflect.DeepEqual(got, want) {
						t.Errorf("event details changed: got %#v; want %#v", got, want)
					}
				}
			})
		}
	}
}
