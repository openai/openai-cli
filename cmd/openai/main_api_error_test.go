package main

import (
	"encoding/json"
	"fmt"
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
			t.Run(test.name+"/"+flag, func(t *testing.T) {
				result := runMainAPIErrorResponse(t, test.status, test.contentType, test.body, flag, "json")
				var payload struct {
					StatusCode int    `json:"status_code"`
					Message    string `json:"message"`
				}
				if err := json.Unmarshal([]byte(result.stderr), &payload); err != nil {
					t.Fatalf("stderr is not a JSON error: %v; %q", err, result.stderr)
				}
				wantMessage := fmt.Sprintf("HTTP %d %s: the server returned no usable JSON error details.", test.status, http.StatusText(test.status))
				if payload.StatusCode != test.status || payload.Message != wantMessage {
					t.Errorf("error = %+v, want status=%d message=%q", payload, test.status, wantMessage)
				}
				if strings.Contains(result.stderr, "synthetic private proxy detail") {
					t.Error("fallback included the unstructured response body")
				}
			})
		}
	}
}

func TestMainAPIErrorsPreserveJSONDetailsAndExtraction(t *testing.T) {
	const details = `{"message":"synthetic error","type":"invalid_request_error","code":"synthetic_code","param":null,"future_field":{"sequence":9007199254740993}}`
	for _, flag := range []string{"--format-error", "--format"} {
		t.Run(flag, func(t *testing.T) {
			result := runMainAPIErrorResponse(t, http.StatusBadRequest, "application/json", `{"error":`+details+`}`, flag, "json")
			if !json.Valid([]byte(result.stderr)) {
				t.Fatalf("stderr is not JSON: %q", result.stderr)
			}
			var got, want any
			for _, input := range []struct {
				text   string
				target *any
			}{{result.stderr, &got}, {details, &want}} {
				decoder := json.NewDecoder(strings.NewReader(input.text))
				decoder.UseNumber()
				if err := decoder.Decode(input.target); err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("JSON error fields changed: got %s; want %s", result.stderr, details)
			}
		})
	}
	result := runMainAPIErrorResponse(t, http.StatusBadRequest, "application/json", `{"error":`+details+`}`, "--format-error", "json", "--transform-error", "future_field.sequence")
	if strings.TrimSpace(result.stderr) != "9007199254740993" {
		t.Errorf("error extraction changed: %q", result.stderr)
	}
}

func runMainAPIErrorResponse(t *testing.T, status int, contentType, body string, flags ...string) mainDispatchResult {
	t.Helper()
	return runMainCommandAPIErrorResponse(t, status, contentType, body, []string{"models", "retrieve", "--model", "synthetic-model"}, flags...)
}

// SDK decoding failures are not *openai.Error values. Their presentation still
// needs to honor the format selected for errors, across generated commands.
func TestMainAPIErrorsWithUnexpectedJSONShapes(t *testing.T) {
	for _, details := range []string{
		`"synthetic private response detail"`, `42`, `true`, `[]`,
		`{"message":42}`, `{"type":false}`, `{"code":[]}`, `{"param":{}}`,
	} {
		for _, format := range []string{"json", "jsonl", "raw", "yaml"} {
			t.Run(details+"/"+format, func(t *testing.T) {
				result := runMainAPIErrorResponse(t, http.StatusBadGateway, "application/json", `{"error":`+details+`}`, "--format-error", format)
				var payload map[string]any
				if strings.HasPrefix(details, "{") {
					// The SDK retains JSON objects even when individual known fields
					// have unexpected types. Keep those original details unchanged.
					payload = decodeMainErrorObject(t, format, result.stderr)
					var want map[string]any
					if format == "yaml" {
						if err := yaml.Unmarshal([]byte(details), &want); err != nil {
							t.Fatal(err)
						}
					} else if err := json.Unmarshal([]byte(details), &want); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(payload, want) {
						t.Errorf("original API fields changed: got %#v, want %#v", payload, want)
					}
				} else {
					payload = decodeMainStructuredError(t, format, result.stderr)
				}
				if _, exists := payload["status_code"]; exists {
					t.Errorf("invented an HTTP status for an SDK decoding error: %v", payload)
				}
				if strings.Contains(result.stderr, "synthetic private response detail") || strings.Contains(result.stderr, "synthetic-image-error-key") {
					t.Errorf("structured diagnostic contains private response data: %q", result.stderr)
				}
			})
		}
	}
}

func TestMainAPIErrorsStructuredAcrossCommands(t *testing.T) {
	for _, command := range [][]string{
		{"models", "list"},
		{"images", "generate", "--inline", "off", "--prompt", "synthetic private prompt", "--model", "synthetic-model"},
		{"responses", "create", "--model", "synthetic-model", "--input", "synthetic private input"},
	} {
		for _, format := range []string{"json", "jsonl", "raw", "yaml"} {
			t.Run(strings.Join(command[:2], "/")+"/"+format, func(t *testing.T) {
				result := runMainCommandAPIErrorResponse(t, http.StatusBadGateway, "application/json", `{"error":true}`, command, "--format", format)
				decodeMainStructuredError(t, format, result.stderr)
			})
		}
	}
}

func TestMainAPIErrorsUnexpectedShapeFormatPrecedenceAndExtraction(t *testing.T) {
	const body = `{"error":true}`
	for _, flags := range [][]string{
		{"--format", "text", "--format-error", "json"},
		{"--format", "yaml", "--format-error", "json"},
		{"--format-error", "JSON"},
	} {
		t.Run(strings.Join(flags, "/"), func(t *testing.T) {
			result := runMainAPIErrorResponse(t, http.StatusBadGateway, "application/json", body, flags...)
			decodeMainStructuredError(t, "json", result.stderr)
		})
	}
	for _, format := range []string{"auto", "text"} {
		t.Run("readable override/"+format, func(t *testing.T) {
			result := runMainAPIErrorResponse(t, http.StatusBadGateway, "application/json", body, "--format", "json", "--format-error", format)
			if strings.TrimSpace(result.stderr) == "" || json.Valid([]byte(result.stderr)) {
				t.Errorf("expected a readable error, got %q", result.stderr)
			}
		})
	}
	result := runMainAPIErrorResponse(t, http.StatusBadGateway, "application/json", body, "--format-error", "json", "--transform-error", "message")
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
	var payload map[string]any
	var err error
	if format == "yaml" {
		err = yaml.Unmarshal([]byte(output), &payload)
	} else {
		err = json.Unmarshal([]byte(output), &payload)
	}
	if err != nil {
		t.Fatalf("stderr is not a %s error object: %v; %q", format, err, output)
	}
	if format == "jsonl" && strings.Count(output, "\n") != 1 {
		t.Errorf("JSONL error spans multiple lines: %q", output)
	}
	return payload
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
	result := runMainImageErrorProcess(t, "pipes", args)
	if result.code != 1 || requests.Load() != 1 || result.stdout != "" {
		t.Fatalf("exit=%d requests=%d stdout=%q, want exit=1 requests=1 empty stdout; stderr=%q", result.code, requests.Load(), result.stdout, result.stderr)
	}
	return result
}
