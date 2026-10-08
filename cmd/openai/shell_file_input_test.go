package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func shellFileInput(t *testing.T, data []byte) *os.File {
	t.Helper()
	name := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func runShellFileCommand(t *testing.T, server *httptest.Server, input *os.File, extraEnv []string, args ...string) mainDispatchResult {
	t.Helper()
	home := t.TempDir()
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home, "OPENAI_API_KEY=sk-fake-shell-input", "OPENAI_BASE_URL=" + server.URL, "FORCE_COLOR=0"}
	env = append(env, extraEnv...)
	return runMainDispatchWithStdin(t, "", env, input, append([]string{"openai", "--format", "json"}, args...)...)
}

func TestMainShellExplicitTextInput(t *testing.T) {
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_synthetic","object":"response","status":"completed","output":[]}`)
	}))
	defer server.Close()
	for _, tc := range []struct{ name, reference, input, want string }{
		{"plain", "@-", "hello\n", "hello\n"},
		{"JSON stays text", "@-", `{"input":"literal","stream":true}`, `{"input":"literal","stream":true}`},
		{"YAML stays text", "@-", "model: literal\n", "model: literal\n"},
		{"empty", "@-", "", ""},
		{"unicode and control", "@file://-", "hello 世界\x00\n", "hello 世界\x00\n"},
		{"base64", "@data://-", "hello\n", base64.StdEncoding.EncodeToString([]byte("hello\n"))},
		{"non UTF8 auto base64", "@-", "\x00\xff\xfe", base64.StdEncoding.EncodeToString([]byte{0, 255, 254})},
		{"stdin content cannot expand files", "@-", "@/synthetic/private.txt", "@/synthetic/private.txt"},
	} {
		for _, untrusted := range []string{"false", "true"} {
			t.Run(tc.name+"/untrusted="+untrusted, func(t *testing.T) {
				got := runShellFileCommand(t, server, shellFileInput(t, []byte(tc.input)), []string{"OPENAI_UNTRUSTED_STDIN=" + untrusted}, "responses", "create", "--model", "model_synthetic", "--input", tc.reference)
				if got.code != 0 || got.stderr != "" {
					t.Fatalf("command: %+v", got)
				}
				body := <-requests
				if body["input"] != tc.want || body["model"] != "model_synthetic" || len(body) != 2 {
					t.Fatalf("body=%#v", body)
				}
			})
		}
	}
}

func TestMainShellWholeRequestAndRepeatedFlags(t *testing.T) {
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_synthetic","object":"response","status":"completed","output":[]}`)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name, input string
		flags       []string
		want        map[string]any
	}{
		{"JSON override and unknown null", `{"model":"from-pipe","input":"text","future":null}`, []string{"--model", "model_synthetic"}, map[string]any{"model": "model_synthetic", "input": "text", "future": nil}},
		{"YAML", "model: model_synthetic\ninput: text\nfuture: null\n", nil, map[string]any{"model": "model_synthetic", "input": "text", "future": nil}},
		{"last scalar releases stdin", `{"model":"model_synthetic"}`, []string{"--input", "@-", "--input", "literal"}, map[string]any{"model": "model_synthetic", "input": "literal"}},
		{"last scalar selects stdin", "exact text\n", []string{"--model", "model_synthetic", "--input", "literal", "--input", "@-"}, map[string]any{"model": "model_synthetic", "input": "exact text\n"}},
		{"untrusted reference remains literal", `{"model":"model_synthetic","input":"@/synthetic/private.txt"}`, nil, map[string]any{"model": "model_synthetic", "input": "@/synthetic/private.txt"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"responses", "create"}, tc.flags...)
			got := runShellFileCommand(t, server, shellFileInput(t, []byte(tc.input)), []string{"OPENAI_UNTRUSTED_STDIN=true"}, args...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("command: %+v", got)
			}
			if body := <-requests; !reflect.DeepEqual(body, tc.want) {
				t.Fatalf("body=%#v want=%#v", body, tc.want)
			}
		})
	}
}

func TestMainShellBinaryStdinAndLiteralPaths(t *testing.T) {
	payload := []byte{0, 255, 254, 'a', '\n'}
	type part struct {
		name, filename, contentType string
		data                        []byte
	}
	requests := make(chan []part, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			http.Error(w, "bad multipart", 400)
			return
		}
		var parts []part
		for {
			p, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Error(err)
				break
			}
			b, err := io.ReadAll(p)
			if err != nil {
				t.Error(err)
			}
			parts = append(parts, part{p.FormName(), p.FileName(), p.Header.Get("Content-Type"), b})
		}
		requests <- parts
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"file_synthetic","object":"file","text":"synthetic"}`)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name  string
		args  []string
		field string
	}{
		{"files", []string{"files", "create", "--purpose", "user_data"}, "file"},
		{"transcription", []string{"audio", "transcriptions", "create", "--model", "whisper-1"}, "file"},
		{"translation", []string{"audio", "translations", "create", "--model", "whisper-1"}, "file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, source := range []string{"stdin", "literal at", "literal dash"} {
				t.Run(source, func(t *testing.T) {
					input := shellFileInput(t, nil)
					path, filename := "-", "anonymous_file"
					if source == "stdin" {
						input = shellFileInput(t, payload)
					} else {
						filename = "@sample.wav"
						if source == "literal dash" {
							filename = "-"
						}
						path = filepath.Join(t.TempDir(), filename)
						if err := os.WriteFile(path, payload, 0600); err != nil {
							t.Fatal(err)
						}
					}
					got := runShellFileCommand(t, server, input, []string{"OPENAI_UNTRUSTED_STDIN=true"}, append(tc.args, "--"+tc.field, path)...)
					if got.code != 0 || got.stderr != "" {
						t.Fatalf("command: %+v", got)
					}
					found := false
					for _, p := range <-requests {
						if p.name == tc.field {
							found = true
							if p.filename != filename || !bytes.Equal(p.data, payload) {
								t.Fatalf("part=%+v", p)
							}
							if source == "stdin" && p.contentType != "application/octet-stream" {
								t.Fatalf("content type=%s", p.contentType)
							}
						}
					}
					if !found {
						t.Fatal("missing upload part")
					}
				})
			}
		})
	}
}

func TestMainShellStdinConflictBeforeRead(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); http.Error(w, "unexpected", 500) }))
	defer server.Close()
	for _, args := range [][]string{
		{"responses", "create", "--model", "model_synthetic", "--input", "@-", "--instructions", "@-"},
		{"audio", "transcriptions", "create", "--model", "whisper-1", "--file", "-", "--prompt", "@-"},
		{"images", "edit", "--model", "dall-e-2", "--prompt", "synthetic", "--image", "-", "--image", "-"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			read, write, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer read.Close()
			defer write.Close()
			got := runShellFileCommand(t, server, read, nil, args...)
			if got.code == 0 || got.stdout != "" || !strings.Contains(got.stderr, "multiple request parameters use stdin") {
				t.Fatalf("command=%+v", got)
			}
			if requests.Load() != 0 {
				t.Fatal("request sent before conflict rejection")
			}
		})
	}
}

func TestMainShellStreamingInputDispatch(t *testing.T) {
	for _, tc := range []struct {
		name, body, stream string
		flags              []string
	}{
		{"JSON true", `{"model":"fake-model","input":"synthetic","stream":true}`, "true", nil},
		{"YAML true", "model: fake-model\ninput: synthetic\nstream: true\n", "true", nil},
		{"explicit false", `{"model":"fake-model","input":"synthetic","stream":true}`, "false", []string{"--stream=false"}},
		{"explicit null", `{"model":"fake-model","input":"synthetic","stream":true}`, "null", []string{"--stream=null"}},
		{"body false", `{"model":"fake-model","input":"synthetic","stream":false}`, "false", nil},
		{"body null", `{"model":"fake-model","input":"synthetic","stream":null}`, "null", nil},
		{"omitted", `{"model":"fake-model","input":"synthetic"}`, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan map[string]json.RawMessage, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				requests <- body
				if string(body["stream"]) == "true" {
					w.Header().Set("Content-Type", "text/event-stream")
					writeStreamingTextEvent(w, `{"type":"response.completed","response":{"id":"resp_synthetic","status":"completed","output":[]}}`)
					io.WriteString(w, "data: [DONE]\n\n")
				} else {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"id":"resp_synthetic","status":"completed","output":[]}`)
				}
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "request.json")
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			stdin, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			args := append([]string{"openai", "responses", "create", "--format", "jsonl"}, tc.flags...)
			got := runMainDispatchWithStdin(t, "", []string{"OPENAI_API_KEY=sk-fake-f10-input", "OPENAI_BASE_URL=" + server.URL, "OPENAI_UNTRUSTED_STDIN=1"}, stdin, args...)
			if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "resp_synthetic") {
				t.Fatalf("got=%+v", got)
			}
			if len(requests) != 1 {
				t.Fatalf("request count=%d", len(requests))
			}
			body := <-requests
			if string(body["stream"]) != tc.stream || string(body["input"]) != `"synthetic"` {
				t.Fatalf("body=%s", body)
			}
		})
	}
}

func TestMainShellMultipartStreamingInputDispatch(t *testing.T) {
	file := filepath.ToSlash(filepath.Join(t.TempDir(), "synthetic.wav"))
	if err := os.WriteFile(file, []byte{0, 255, 'a'}, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, stream, wire string
		flags              []string
		streaming          bool
	}{
		{"body true", `true`, "true", nil, true},
		{"body false", `false`, "false", nil, false},
		{"body null", `null`, "", nil, false},
		{"quoted true remains scalar", `"true"`, "true", nil, false},
		{"explicit false", `true`, "false", []string{"--stream=false"}, false},
		{"explicit true", `false`, "true", []string{"--stream=true"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captured := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reader, err := r.MultipartReader()
				if err != nil {
					t.Error(err)
					http.Error(w, "bad multipart", 400)
					return
				}
				stream := ""
				for {
					part, err := reader.NextPart()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Error(err)
						break
					}
					data, err := io.ReadAll(part)
					if err != nil {
						t.Error(err)
					}
					if part.FormName() == "stream" {
						stream = string(data)
					}
				}
				captured <- stream
				if tc.streaming {
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, "data: {\"type\":\"transcript.text.done\",\"text\":\"synthetic transcript\"}\n\ndata: [DONE]\n\n")
				} else {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"text":"synthetic transcript"}`)
				}
			}))
			defer server.Close()
			body := `{"model":"whisper-1","stream":` + tc.stream + `}`
			args := append([]string{"audio", "transcriptions", "create", "--file", file}, tc.flags...)
			got := runShellFileCommand(t, server, shellFileInput(t, []byte(body)), []string{"OPENAI_UNTRUSTED_STDIN=true"}, args...)
			if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "synthetic transcript") {
				t.Fatalf("command=%+v", got)
			}
			if wire := <-captured; wire != tc.wire {
				t.Fatalf("stream wire=%q want=%q", wire, tc.wire)
			}
		})
	}
}
