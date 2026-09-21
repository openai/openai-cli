package cli_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestMainReadableBodylessSuccessConfirmations(t *testing.T) {
	for _, test := range []struct {
		name, route, want string
		args              []string
	}{
		{"response", "DELETE /responses/resp_synthetic", `Deleted response "resp_synthetic".`, []string{"responses", "delete", "resp_synthetic"}},
		{"beta response", "DELETE /responses/resp_synthetic", `Deleted response "resp_synthetic".`, []string{"beta:responses", "delete", "resp_synthetic"}},
		{"container", "DELETE /containers/cntr_synthetic", `Deleted container "cntr_synthetic".`, []string{"containers", "delete", "cntr_synthetic"}},
		{"container file", "DELETE /containers/cntr_synthetic/files/file_synthetic", `Deleted container file "file_synthetic".`, []string{"containers:files", "delete", "cntr_synthetic", "file_synthetic"}},
		{"accept call", "POST /realtime/calls/call_synthetic/accept", `Accepted call "call_synthetic".`, []string{"realtime:calls", "accept", "call_synthetic"}},
		{"end call", "POST /realtime/calls/call_synthetic/hangup", `Ended call "call_synthetic".`, []string{"realtime:calls", "hangup", "call_synthetic"}},
		{"transfer call", "POST /realtime/calls/call_synthetic/refer", `Requested transfer for call "call_synthetic".`, []string{"realtime:calls", "refer", "call_synthetic", "--target-uri", "sip:synthetic@example.invalid"}},
		{"reject call", "POST /realtime/calls/call_synthetic/reject", `Rejected call "call_synthetic".`, []string{"realtime:calls", "reject", "call_synthetic"}},
		{"accept session", "POST /live/sessions/sess_synthetic/accept", `Accepted session "sess_synthetic".`, []string{"live:sessions", "accept", "sess_synthetic", "--session", `{"type":"live","model":"synthetic"}`}},
		{"end session", "POST /live/sessions/sess_synthetic/hangup", `Ended session "sess_synthetic".`, []string{"live:sessions", "hangup", "sess_synthetic"}},
		{"transfer session", "POST /live/sessions/sess_synthetic/refer", `Requested transfer for session "sess_synthetic".`, []string{"live:sessions", "refer", "sess_synthetic", "--target-uri", "sip:synthetic@example.invalid"}},
		{"reject session", "POST /live/sessions/sess_synthetic/reject", `Rejected session "sess_synthetic".`, []string{"live:sessions", "reject", "sess_synthetic", "--status-code", "603"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := readableTestServer(t, test.route, "application/json", "", http.StatusNoContent)
			got := runReadableMain(t, server.URL, nil, test.args...)
			assertReadableSuccess(t, got, test.want)
			if got.stdout != test.want+"\n" {
				t.Fatalf("unexpected confirmation: %q", got.stdout)
			}
			for _, format := range []string{"json", "raw"} {
				args := append([]string{"--format", format}, test.args...)
				got := runReadableMain(t, server.URL, nil, args...)
				assertReadableProcessSuccess(t, got)
				if got.stdout != "" {
					t.Fatalf("%s invented an API payload: %q", format, got.stdout)
				}
			}
		})
	}
}

func TestMainReadableBodylessFailureDoesNotConfirmSuccess(t *testing.T) {
	server := readableTestServer(t, "DELETE /responses/resp_synthetic", "application/json", `{"error":{"message":"synthetic private input","param":"response_id","type":"invalid_request_error","code":"not_found"}}`, http.StatusNotFound)
	got := runReadableMain(t, server.URL, nil, "responses", "delete", "resp_synthetic")
	if got.code == 0 || got.stdout != "" || !strings.Contains(got.stderr, "Request failed (404") {
		t.Fatalf("failed delete reported success: %+v", got)
	}
}

func TestMainReadableAPIArgumentGuidance(t *testing.T) {
	for _, test := range []struct {
		name, route, parameter, code string
		status                       int
		args, want                   []string
	}{
		{"encoding choice", "POST /embeddings", "encoding_format", "invalid_value", 400,
			[]string{"embeddings", "create", "--model", "model-synthetic", "--input", "synthetic"}, []string{"--encoding-format", "float or base64", "help --all embeddings create"}},
		{"nested indexed field", "POST /chat/completions", "messages[0].content", "invalid_type", 422,
			[]string{"chat:completions", "create", "--model", "model-synthetic", "--message", `{"role":"user","content":"synthetic"}`}, []string{"--message", "Check the value's type", "help --all chat:completions create"}},
		{"nested command flag", "POST /live/sessions/sess_synthetic/accept", "session.model", "invalid_value", 400,
			[]string{"live:sessions", "accept", "sess_synthetic", "--session", `{"type":"live","model":"synthetic"}`}, []string{"--session.model", "help --all live:sessions accept"}},
		{"required value", "POST /embeddings", "input", "missing_required_parameter", 400,
			[]string{"embeddings", "create", "--model", "model-synthetic", "--input", "synthetic"}, []string{"Add --input with a value"}},
		{"unsupported field", "POST /responses", "temperature", "unsupported_parameter", 400,
			[]string{"responses", "create", "--model", "model-synthetic", "--input", "synthetic"}, []string{"does not support --temperature", "Remove it or choose a model"}},
		{"model context", "POST /responses", "input", "context_length_exceeded", 400,
			[]string{"responses", "create", "--model", "model-synthetic", "--input", "synthetic"}, []string{"Shorten the input or conversation"}},
		{"unknown sensitive param", "POST /embeddings", "input\x1b]52;c;private-secret\a", "private-code", 400,
			[]string{"embeddings", "create", "--model", "model-synthetic", "--input", "synthetic"}, []string{"The API rejected the request", "help --all embeddings create"}},
		{"wrong command flag", "POST /embeddings", "temperature", "invalid_value", 400,
			[]string{"embeddings", "create", "--model", "model-synthetic", "--input", "synthetic"}, []string{"The API rejected the request"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			apiError := map[string]any{"message": "private-secret rejected input\x1b]52;c;private-secret\a", "param": test.parameter, "code": test.code, "type": "invalid_request_error"}
			payload, _ := json.Marshal(map[string]any{"error": apiError})
			server := readableTestServer(t, test.route, "application/json", string(payload), test.status)
			got := runReadableMain(t, server.URL, nil, test.args...)
			if got.code == 0 || got.stdout != "" {
				t.Fatalf("request failure not preserved: %+v", got)
			}
			for _, want := range test.want {
				if !strings.Contains(got.stderr, want) {
					t.Errorf("missing %q: %s", want, got.stderr)
				}
			}
			for _, private := range []string{"private-secret", "private-code", "\x1b", "\a"} {
				if strings.Contains(got.stderr, private) {
					t.Errorf("untrusted details shown: %q", got.stderr)
				}
			}
			if test.name == "wrong command flag" && strings.Contains(got.stderr, "--temperature") {
				t.Errorf("suggested a flag absent from this command: %q", got.stderr)
			}
			args := append([]string{"--format", "json"}, test.args...)
			got = runReadableMain(t, server.URL, nil, args...)
			if got.code == 0 || got.stdout != "" {
				t.Fatalf("JSON error exit changed: %+v", got)
			}
			body, _ := json.Marshal(apiError)
			assertReadableJSONValues(t, got.stderr, string(body))
		})
	}
}
