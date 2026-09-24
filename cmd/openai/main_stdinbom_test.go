package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Windows tools such as PowerShell 5.1's Set-Content -Encoding UTF8 start UTF-8
// files with a byte order mark. Piped request data must be read the same way
// with or without one.
func TestMainStdinByteOrderMark(t *testing.T) {
	type request struct {
		path string
		body map[string]any
	}
	requests := make(chan request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode request body: %v", err)
			}
		}
		requests <- request{path: r.URL.Path, body: body}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_test","object":"response","status":"completed","output":[]}`)
	}))
	t.Cleanup(server.Close)

	bom := string(rune(0xFEFF))
	for _, tc := range []struct {
		name     string
		stdin    string
		args     []string
		wantPath string
		wantBody map[string]any
	}{
		{
			name:     "JSON body merged with a flag",
			stdin:    bom + `{"model":"test-model","input":"test input"}`,
			args:     []string{"responses", "create", "--instructions", "test instructions"},
			wantPath: "/responses",
			wantBody: map[string]any{"model": "test-model", "input": "test input", "instructions": "test instructions"},
		},
		{
			name: "YAML body",
			stdin: bom + `model: test-model
input: test input
`,
			args:     []string{"responses", "create"},
			wantPath: "/responses",
			wantBody: map[string]any{"model": "test-model", "input": "test input"},
		},
		{
			name:     "JSON path parameter",
			stdin:    bom + `{"response_id":"resp_test"}`,
			args:     []string{"responses", "retrieve"},
			wantPath: "/responses/resp_test",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "request")
			if err := os.WriteFile(path, []byte(tc.stdin), 0o600); err != nil {
				t.Fatal(err)
			}
			stdin, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()

			args := append([]string{"openai", "--base-url", server.URL, "--api-key", "sk-test", "--format", "jsonl"}, tc.args...)
			got := runMainDispatchWithStdin(t, "", nil, stdin, args...)
			if got.code != 0 {
				t.Fatalf("main = %+v, want exit code 0", got)
			}

			select {
			case req := <-requests:
				if req.path != tc.wantPath {
					t.Errorf("request path = %q, want %q", req.path, tc.wantPath)
				}
				if tc.wantBody != nil && !reflect.DeepEqual(req.body, tc.wantBody) {
					t.Errorf("request body = %#v, want %#v", req.body, tc.wantBody)
				}
			default:
				t.Fatal("main sent no request, want one")
			}
		})
	}
}
