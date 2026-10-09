package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const requestIDErrorBody = `{"error":{"message":"synthetic response detail","type":"server_error","future_field":"kept"}}`

func runRequestIDError(t *testing.T, requestID string, reflectRequestID bool, flags ...string) mainDispatchResult {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-should-retry", "false")
		w.Header().Set("X-Private-Diagnostic", "synthetic-private-other-header")
		returnedID := requestID
		if reflectRequestID {
			returnedID = r.Header.Get("X-Request-ID")
			if returnedID != "synthetic-private-reflected" {
				t.Error("custom request ID did not reach the API fixture")
			}
		}
		if returnedID != "" {
			w.Header().Set("X-Request-ID", returnedID)
		}
		w.WriteHeader(http.StatusInternalServerError)
		if _, err := io.WriteString(w, requestIDErrorBody); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	args := append([]string{"openai"}, flags...)
	args = append(args, "models", "retrieve", "model_synthetic")
	got := runMainDispatchWithEnv(t, "bash", []string{
		"OPENAI_BASE_URL=" + server.URL,
		"OPENAI_API_KEY=synthetic-private-request-id-key",
	}, args...)
	if got.code != 1 || got.stdout != "" || requests.Load() != 1 {
		t.Fatalf("request-ID presentation changed status, data, or requests: %+v; requests=%d", got, requests.Load())
	}
	return got
}

func TestMainDispatchOutputAPIErrorRequestID(t *testing.T) {
	const requestID = "req_abc123"
	for _, tc := range []struct {
		name, responseID string
		flags            []string
		wantID           bool
		reflectID        bool
	}{
		{"ordinary", requestID, nil, true, false},
		{"quiet", requestID, []string{"--quiet"}, true, false},
		{"missing", "", nil, false, false},
		{"invalid", "https://synthetic.invalid/?token=synthetic-private-header", nil, false, false},
		{"oversized", strings.Repeat("a", 257), nil, false, false},
		{"custom header reflection", "synthetic-private-reflected", []string{"--header", "X-Request-ID: synthetic-private-reflected"}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runRequestIDError(t, tc.responseID, tc.reflectID, tc.flags...)
			prefix := "HTTP 500: Internal Server Error.\n"
			if tc.wantID {
				prefix += "Request ID: " + tc.responseID + "\n"
			}
			if !strings.HasPrefix(got.stderr, prefix) || strings.Contains(got.stderr, "Request ID:") != tc.wantID {
				t.Fatalf("request ID is missing, misplaced, or should be omitted: %q", got.stderr)
			}
			for _, private := range []string{"synthetic-private-", "synthetic response detail", "https://", "\x1b"} {
				if strings.Contains(got.stderr, private) {
					t.Fatalf("human error exposed unrelated or unsafe data: %q", got.stderr)
				}
			}
			if !strings.Contains(got.stderr, "The API may have received the request.") ||
				!strings.Contains(got.stderr, "Check its status before trying again.") {
				t.Fatalf("request-ID display lost recovery guidance: %q", got.stderr)
			}
		})
	}
}

func TestMainDispatchOutputRequestIDKeepsMachineErrors(t *testing.T) {
	for _, flags := range [][]string{
		{"--format-error", "json"}, {"--format-error", "jsonl"},
		{"--format-error", "yaml"}, {"--format-error", "raw"},
		{"--format-error", "pretty"}, {"--format-error", "explore"},
		{"--format-error", "json", "--transform-error", "future_field"},
		{"--format-error", "text", "--transform-error", "future_field"},
	} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			withoutID := runRequestIDError(t, "", false, flags...)
			withID := runRequestIDError(t, "req_abc123", false, flags...)
			if withID != withoutID || strings.Contains(withID.stderr, "req_abc123") {
				t.Fatalf("request-ID metadata changed machine or extracted error bytes: without=%+v with=%+v", withoutID, withID)
			}
		})
	}
}
