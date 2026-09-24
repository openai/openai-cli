package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Python writes 0.00001 as 1e-05, and JavaScript and Go write 0.0000001 as
// 1e-7. The request must carry both as JSON numbers whether they arrive on
// stdin or in a JSON flag value.
func TestMainJSONExponentNumbers(t *testing.T) {
	bodies := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		var body map[string]any
		if err := decoder.Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		bodies <- body
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_test","object":"response","status":"completed","output":[]}`)
	}))
	t.Cleanup(server.Close)

	path := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(path, []byte(`{"model":"test-model","input":"test input","top_p":1e-05}`), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, untrusted := range []string{"false", "true"} {
		t.Run("untrusted_stdin_"+untrusted, func(t *testing.T) {
			stdin, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()

			got := runMainDispatchWithStdin(t, "", []string{"OPENAI_UNTRUSTED_STDIN=" + untrusted}, stdin,
				"openai", "--base-url", server.URL, "--api-key", "sk-test", "--format", "jsonl",
				"responses", "create",
				"--text", `{"format":{"type":"json_schema","name":"score","schema":{"type":"number","minimum":1e-7}}}`)
			if got.code != 0 {
				t.Fatalf("main = %+v, want exit code 0", got)
			}

			var body map[string]any
			select {
			case body = <-bodies:
			default:
				t.Fatal("main sent no request, want one")
			}
			if body["model"] != "test-model" {
				t.Errorf("request model = %#v, want test-model from stdin", body["model"])
			}
			assertJSONNumber(t, "top_p", body["top_p"], 1e-05)
			text, _ := body["text"].(map[string]any)
			format, _ := text["format"].(map[string]any)
			schema, _ := format["schema"].(map[string]any)
			assertJSONNumber(t, "text.format.schema.minimum", schema["minimum"], 1e-07)
		})
	}
}

func assertJSONNumber(t *testing.T, name string, got any, want float64) {
	t.Helper()
	number, ok := got.(json.Number)
	if !ok {
		t.Errorf("request %s = %#v (%T), want JSON number %g", name, got, got, want)
		return
	}
	if value, err := number.Float64(); err != nil || value != want {
		t.Errorf("request %s = %s, want %g", name, number, want)
	}
}
