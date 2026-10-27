package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMainUnionInputPreservesLiteralText(t *testing.T) {
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_test","object":"response","status":"completed","output":[]}`)
	}))
	t.Cleanup(server.Close)
	for _, input := range []string{"Translate: hello", "#1 priority", "line one\nline two", "  hello # world  ", "null", "42", `[{"role":"user"}]`, "*Important*: read this"} {
		t.Run(input, func(t *testing.T) {
			got := runMainDispatch(t, "bash", "openai", "--base-url", server.URL, "--api-key", "sk-test", "--format", "json", "responses", "create", "--model", "test-model", "--input", input)
			if got.code != 0 {
				t.Fatalf("main = %+v", got)
			}
			select {
			case body := <-requests:
				if !reflect.DeepEqual(body["input"], input) {
					t.Errorf("input = %#v, want %#v", body["input"], input)
				}
			default:
				t.Fatal("no request received")
			}
		})
	}
}

func TestMainUnionStructuredInput(t *testing.T) {
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_test","object":"response","status":"completed","output":[]}`)
	}))
	t.Cleanup(server.Close)
	cases := []struct {
		name, input string
		want        any
	}{
		{"json array", `[{"role":"user","content":"hello"}]`, []any{map[string]any{"role": "user", "content": "hello"}}},
		{"yaml array", "- role: user\n  content: hello", []any{map[string]any{"role": "user", "content": "hello"}}},
		{"null", "null", nil},
		{"number", "42", float64(42)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runMainDispatch(t, "bash", "openai", "--base-url", server.URL, "--api-key", "sk-test", "--format", "json", "responses", "create", "--model", "test-model", "--input-structured", tc.input)
			if got.code != 0 {
				t.Fatalf("main = %+v", got)
			}
			select {
			case body := <-requests:
				if !reflect.DeepEqual(body["input"], tc.want) {
					t.Errorf("input = %#v, want %#v", body["input"], tc.want)
				}
			default:
				t.Fatal("no request received")
			}
		})
	}
	t.Run("malformed", func(t *testing.T) {
		got := runMainDispatch(t, "bash", "openai", "--base-url", server.URL, "--api-key", "sk-test", "responses", "create", "--model", "test-model", "--input-structured", "[bad")
		if got.code == 0 {
			t.Fatalf("main = %+v, want failure", got)
		}
		if !strings.Contains(got.stderr, "Expected valid JSON or YAML") {
			t.Fatalf("missing structured input guidance: %s", got.stderr)
		}
		select {
		case <-requests:
			t.Fatal("malformed value sent a request")
		default:
		}
	})
}

func TestMainUnionInputRejectsConflictingModes(t *testing.T) {
	requests := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- struct{}{}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_test","object":"response","status":"completed","output":[]}`)
	}))
	t.Cleanup(server.Close)
	for _, args := range [][]string{
		{"--input-structured", `["first"]`, "--input", "second"},
		{"--input", "second", "--input-structured", `["first"]`},
	} {
		t.Run(args[0], func(t *testing.T) {
			base := []string{"openai", "--base-url", server.URL, "--api-key", "sk-test", "responses", "create", "--model", "test-model"}
			got := runMainDispatch(t, "bash", append(base, args...)...)
			if got.code == 0 {
				t.Fatalf("conflicting flags succeeded: %+v", got)
			}
			if !strings.Contains(got.stderr, "Use either --input or --input-structured") {
				t.Fatalf("missing conflict guidance: %s", got.stderr)
			}
			select {
			case <-requests:
				t.Fatal("conflicting flags sent a request")
			default:
			}
		})
	}
}

func TestMainUnionNestedInputRejectsLiteralAndKeepsStructuredFields(t *testing.T) {
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"run_test","object":"thread.run","status":"queued"}`)
	}))
	t.Cleanup(server.Close)
	cases := []struct {
		name    string
		args    []string
		success bool
	}{
		{"literal then nested", []string{"--additional-message", "[]", "--additional-message.role", "user"}, false},
		{"nested then literal", []string{"--additional-message.role", "user", "--additional-message", "[]"}, false},
		{"structured then nested", []string{"--additional-message-structured", "[]", "--additional-message.role", "user"}, true},
		{"nested then structured", []string{"--additional-message.role", "user", "--additional-message-structured", "[]"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := []string{"openai", "--base-url", server.URL, "--api-key", "sk-test", "--format", "json", "beta", "threads", "runs", "create", "--thread-id", "thread_test", "--assistant-id", "asst_test"}
			got := runMainDispatch(t, "bash", append(base, tc.args...)...)
			if (got.code == 0) != tc.success {
				t.Fatalf("main = %+v, want success %t", got, tc.success)
			}
			select {
			case body := <-requests:
				if !tc.success {
					t.Fatal("conflicting flags sent a request")
				}
				want := []any{map[string]any{"role": "user"}}
				if !reflect.DeepEqual(body["additional_messages"], want) {
					t.Errorf("additional_messages = %#v, want %#v", body["additional_messages"], want)
				}
			default:
				if tc.success {
					t.Fatal("no request received")
				}
			}
		})
	}
}

func TestMainUnionStopAndSearchQuery(t *testing.T) {
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[]}`)
	}))
	t.Cleanup(server.Close)
	cases := []struct {
		name      string
		args      []string
		key, want string
	}{
		{"stop hash", []string{"completions", "create", "--model", "test-model", "--prompt", "hello", "--stop", "###"}, "stop", "###"},
		{"stop colon", []string{"completions", "create", "--model", "test-model", "--prompt", "hello", "--stop", "Human:"}, "stop", "Human:"},
		{"search", []string{"vector-stores", "search", "--vector-store-id", "vs_test", "--query", "error: timeout"}, "query", "error: timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := []string{"openai", "--base-url", server.URL, "--api-key", "sk-test", "--format", "json"}
			got := runMainDispatch(t, "bash", append(base, tc.args...)...)
			if got.code != 0 {
				t.Fatalf("main = %+v", got)
			}
			select {
			case body := <-requests:
				if body[tc.key] != tc.want {
					t.Errorf("%s = %#v, want %q", tc.key, body[tc.key], tc.want)
				}
			default:
				t.Fatal("no request received")
			}
		})
	}
}

func TestMainUnionRepeatedStructuredArray(t *testing.T) {
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_test","object":"response","status":"completed","output":[]}`)
	}))
	t.Cleanup(server.Close)
	args := []string{"openai", "--base-url", server.URL, "--api-key", "sk-test", "--format", "json", "responses", "create", "--model", "test-model", "--input-structured", `[{"role":"user","content":"first"}]`, "--input-structured", `{"role":"user","content":"second"}`}
	got := runMainDispatch(t, "bash", args...)
	if got.code != 0 {
		t.Fatalf("main = %+v", got)
	}
	select {
	case body := <-requests:
		want := []any{map[string]any{"role": "user", "content": "first"}, map[string]any{"role": "user", "content": "second"}}
		if !reflect.DeepEqual(body["input"], want) {
			t.Errorf("input = %#v, want %#v", body["input"], want)
		}
	default:
		t.Fatal("no request received")
	}
}

func TestMainUnionPipedInputAndExplicitOverride(t *testing.T) {
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_test","object":"response","status":"completed","output":[]}`)
	}))
	t.Cleanup(server.Close)
	cases := []struct {
		name, data string
		flags      []string
		want       any
	}{
		{"json array", `{"input":[{"role":"user","content":"hello"}]}`, nil, []any{map[string]any{"role": "user", "content": "hello"}}},
		{"yaml array", "input:\n  - role: user\n    content: hello\n", nil, []any{map[string]any{"role": "user", "content": "hello"}}},
		{"literal override", `{"input":["piped"]}`, []string{"--input", "null"}, "null"},
		{"structured override", `{"input":"piped"}`, []string{"--input-structured", `["explicit"]`}, []any{"explicit"}},
	}
	for _, tc := range cases {
		for _, untrusted := range []string{"0", "1"} {
			t.Run(tc.name+"/untrusted="+untrusted, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "input")
				if err := os.WriteFile(path, []byte(tc.data), 0600); err != nil {
					t.Fatal(err)
				}
				stdin, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer stdin.Close()
				args := []string{"openai", "--base-url", server.URL, "--api-key", "sk-test", "--format", "json", "responses", "create", "--model", "test-model"}
				got := runMainDispatchWithStdin(t, "", []string{"OPENAI_UNTRUSTED_STDIN=" + untrusted}, stdin, append(args, tc.flags...)...)
				if got.code != 0 {
					t.Fatalf("main = %+v", got)
				}
				select {
				case body := <-requests:
					if !reflect.DeepEqual(body["input"], tc.want) {
						t.Errorf("input = %#v, want %#v", body["input"], tc.want)
					}
				default:
					t.Fatal("no request received")
				}
			})
		}
	}
}
