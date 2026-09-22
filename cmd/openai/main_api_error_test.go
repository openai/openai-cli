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
	args = append(args, "models", "retrieve", "--model", "synthetic-model")
	result := runMainImageErrorProcess(t, "pipes", args)
	if result.code != 1 || requests.Load() != 1 || result.stdout != "" {
		t.Fatalf("exit=%d requests=%d stdout=%q, want exit=1 requests=1 empty stdout; stderr=%q", result.code, requests.Load(), result.stdout, result.stderr)
	}
	return result
}
