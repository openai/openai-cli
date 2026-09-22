package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMainReadableDefaultsWithRedirectedOutput(t *testing.T) {
	for _, tc := range []struct {
		name, route, payload string
		args, content        []string
	}{
		{
			name: "single model", route: "GET /models/model-synthetic",
			args:    []string{"models", "retrieve", "model-synthetic"},
			payload: `{"id":"model-synthetic","object":"model","created":123,"owned_by":"synthetic-owner"}`,
			content: []string{"model-synthetic", "synthetic-owner"},
		},
		{
			name: "model list", route: "GET /models", args: []string{"models", "list"},
			payload: `{"object":"list","data":[{"id":"model-first","object":"model","owned_by":"first-owner"},{"id":"model-second","object":"model","owned_by":"second-owner"}]}`,
			content: []string{"model-first", "first-owner", "model-second", "second-owner"},
		},
		{
			name: "response text", route: "POST /responses",
			args:    []string{"responses", "create", "--model", "model-synthetic", "--input", "synthetic question"},
			payload: `{"id":"resp_synthetic","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"A readable response.","annotations":[]}]}],"usage":{"total_tokens":9}}`,
			content: []string{"A readable response."},
		},
		{
			name: "chat text", route: "POST /chat/completions",
			args:    []string{"chat:completions", "create", "--model", "model-synthetic", "--message", `{"role":"user","content":"synthetic question"}`},
			payload: `{"id":"chatcmpl_synthetic","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"A readable chat answer."},"finish_reason":"stop"}]}`,
			content: []string{"A readable chat answer."},
		},
		{
			name: "nested fields", route: "GET /models/model-synthetic",
			args:    []string{"models", "retrieve", "model-synthetic"},
			payload: `{"id":"model-synthetic","object":"model","owned_by":"synthetic-owner","capabilities":{"description":"Nested details remain useful","regions":["north","south"]}}`,
			content: []string{"model-synthetic", "Nested details remain useful", "north", "south"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := readableTestServer(t, tc.route, "application/json", tc.payload, http.StatusOK)
			for _, flags := range [][]string{nil, {"--format", "auto"}} {
				args := append(append([]string{}, flags...), tc.args...)
				got := runReadableMain(t, server.URL, nil, args...)
				assertReadableSuccess(t, got, tc.content...)
			}
		})
	}
}

func TestMainReadableExplicitFormatsPreserveOriginalResponse(t *testing.T) {
	const payload = `{"id":"resp_synthetic","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Synthetic answer","annotations":[]}]}],"usage":{"total_tokens":9},"future":{"number":9007199254740993,"values":[true,null,"unmodified"]}}`
	server := readableTestServer(t, "GET /responses/resp_synthetic", "application/json", payload, http.StatusOK)
	for _, format := range []string{"json", "JSON", "jsonl", "raw"} {
		t.Run(format, func(t *testing.T) {
			got := runReadableMain(t, server.URL, nil, "--format", format, "responses", "retrieve", "resp_synthetic")
			assertReadableProcessSuccess(t, got)
			assertReadableJSONValues(t, got.stdout, payload)
		})
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"transform", []string{"--transform", "output.0.content.0.text"}, `"Synthetic answer"`},
		{"raw output", []string{"--raw-output"}, payload},
		{"raw transformed string", []string{"--transform", "output.0.content.0.text", "--raw-output"}, "Synthetic answer\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append(append([]string{}, tc.args...), "responses", "retrieve", "resp_synthetic")
			got := runReadableMain(t, server.URL, nil, args...)
			assertReadableProcessSuccess(t, got)
			if tc.name == "raw transformed string" {
				if got.stdout != tc.want {
					t.Fatalf("raw string changed: got %q; want %q", got.stdout, tc.want)
				}
			} else {
				assertReadableJSONValues(t, got.stdout, tc.want)
			}
		})
	}
}

func TestMainReadableListKeepsExplicitItemAndPageContracts(t *testing.T) {
	const first = `{"id":"model-first","object":"model","owned_by":"first-owner","future":9007199254740993}`
	const second = `{"id":"model-second","object":"model","owned_by":"second-owner","future":null}`
	const payload = `{"object":"list","data":[` + first + `,` + second + `],"future_envelope":"retained in raw mode"}`
	server := readableTestServer(t, "GET /models", "application/json", payload, http.StatusOK)
	for _, format := range []string{"json", "JSON", "jsonl", "raw"} {
		t.Run(format, func(t *testing.T) {
			got := runReadableMain(t, server.URL, nil, "--format", format, "models", "list")
			assertReadableProcessSuccess(t, got)
			if format == "raw" {
				assertReadableJSONValues(t, got.stdout, payload)
			} else {
				assertReadableJSONValues(t, got.stdout, first, second)
			}
		})
	}
}

func TestMainReadableStreamsAndExplicitEvents(t *testing.T) {
	for _, tc := range []struct {
		name, route string
		args        []string
		events      []string
	}{
		{
			name: "responses", route: "POST /responses",
			args: []string{"responses", "create", "--model", "model-synthetic", "--input", "synthetic question", "--stream", "true"},
			events: []string{
				`{"type":"response.output_text.delta","delta":"Hello ","sequence_number":1,"item_id":"msg_synthetic","output_index":0,"content_index":0,"future":9007199254740993}`,
				`{"type":"response.output_text.delta","delta":"from a stream.","sequence_number":2,"item_id":"msg_synthetic","output_index":0,"content_index":0}`,
				`{"type":"response.output_text.done","text":"Hello from a stream.","sequence_number":3,"item_id":"msg_synthetic","output_index":0,"content_index":0}`,
			},
		},
		{
			name: "chat", route: "POST /chat/completions",
			args: []string{"chat:completions", "create", "--model", "model-synthetic", "--message", `{"role":"user","content":"synthetic question"}`, "--stream", "true"},
			events: []string{
				`{"id":"chatcmpl_synthetic","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello "},"finish_reason":null}],"future":9007199254740993}`,
				`{"id":"chatcmpl_synthetic","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"from a stream."},"finish_reason":null}]}`,
				`{"id":"chatcmpl_synthetic","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "data: " + strings.Join(tc.events, "\n\ndata: ") + "\n\ndata: [DONE]\n\n"
			server := readableTestServer(t, tc.route, "text/event-stream", body, http.StatusOK)
			got := runReadableMain(t, server.URL, nil, tc.args...)
			assertReadableSuccess(t, got, "Hello from a stream.")
			if strings.Count(got.stdout, "Hello from a stream.") != 1 {
				t.Errorf("stream repeated completed text: %q", got.stdout)
			}
			for _, format := range []string{"json", "JSON", "jsonl"} {
				args := append([]string{"--format", format}, tc.args...)
				got := runReadableMain(t, server.URL, nil, args...)
				assertReadableProcessSuccess(t, got)
				assertReadableJSONValues(t, got.stdout, tc.events...)
			}
		})
	}
}

func TestMainReadableLongListDoesNotUsePagerWhenRedirected(t *testing.T) {
	var items []string
	for i := range 80 {
		items = append(items, fmt.Sprintf(`{"id":"model-%03d","object":"model","owned_by":"synthetic-owner"}`, i))
	}
	server := readableTestServer(t, "GET /models", "application/json", `{"object":"list","data":[`+strings.Join(items, ",")+`]}`, http.StatusOK)
	got := runReadableMain(t, server.URL, []string{"PAGER=" + filepath.Join(t.TempDir(), "pager-must-not-run")}, "models", "list")
	assertReadableSuccess(t, got, "model-000", "model-079")
	for i := range 80 {
		if !strings.Contains(got.stdout, fmt.Sprintf("model-%03d", i)) {
			t.Errorf("redirected output lost model %d", i)
		}
	}
}

func TestMainReadableErrorsStayOnStderr(t *testing.T) {
	const body = `{"message":"Synthetic model is unavailable","type":"invalid_request_error","code":"model_not_found","param":"model"}`
	const payload = `{"error":` + body + `}`
	server := readableTestServer(t, "GET /models/model-synthetic", "application/json", payload, http.StatusNotFound)
	for _, flags := range [][]string{nil, {"--format-error", "json"}, {"--format-error", "JSON"}} {
		args := append(append([]string{}, flags...), "models", "retrieve", "model-synthetic")
		got := runReadableMain(t, server.URL, nil, args...)
		if got.code == 0 || got.stdout != "" {
			t.Fatalf("API error must fail with details on stderr only: %+v", got)
		}
		if len(flags) == 0 {
			if !strings.Contains(got.stderr, "404") || !strings.Contains(got.stderr, "--format-error json") || strings.Contains(got.stderr, "Synthetic model is unavailable") {
				t.Fatalf("default error must give readable guidance: %q", got.stderr)
			}
		} else {
			assertReadableJSONValues(t, got.stderr, body)
		}
	}
}

func TestMainReadableImagesSaveWithRedirectedOutput(t *testing.T) {
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(imageBytes.Bytes())
	payload := `{"created":123,"data":[{"b64_json":"` + encoded + `"}]}`
	server := readableTestServer(t, "POST /images/generations", "application/json", payload, http.StatusOK)
	home := t.TempDir()
	got := runReadableMain(t, server.URL, []string{"HOME=" + home, "USERPROFILE=" + home}, "images", "generate", "--prompt", "A tiny orange robot")
	assertReadableSuccess(t, got, "tiny-orange-robot.png")
	path := filepath.Join(home, "Downloads", "gpt-images", "tiny-orange-robot.png")
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(saved, imageBytes.Bytes()) || strings.Contains(got.stdout, encoded) {
		t.Fatal("default redirected image output must save the original image and print its location")
	}
	for _, format := range []string{"json", "JSON"} {
		home := t.TempDir()
		got := runReadableMain(t, server.URL, []string{"HOME=" + home, "USERPROFILE=" + home},
			"--format", format, "images", "generate", "--prompt", "A tiny orange robot")
		assertReadableProcessSuccess(t, got)
		assertReadableJSONValues(t, got.stdout, payload)
		if _, err := os.Stat(filepath.Join(home, "Downloads")); !os.IsNotExist(err) {
			t.Errorf("explicit %s created a download folder: %v", format, err)
		}
	}
}

func TestMainReadableImageModelsOffline(t *testing.T) {
	got := runReadableMain(t, "http://127.0.0.1:1", nil, "images", "models", "--offline")
	assertReadableSuccess(t, got, "gpt-image-2.5-sunburst", "gpt-image-2.5-flare")
}

func TestMainReadableTextHonorsExtraction(t *testing.T) {
	t.Run("offline image model", func(t *testing.T) {
		got := runReadableMain(t, "http://127.0.0.1:1", nil, "--format", "text", "--transform", "default_model", "images", "models", "--offline")
		assertReadableProcessSuccess(t, got)
		if got.stdout != "gpt-image-2.5-sunburst\n" {
			t.Fatalf("model extraction included table or guidance: %q", got.stdout)
		}
	})
	t.Run("generated model retrieval", func(t *testing.T) {
		server := readableTestServer(t, "GET /models/model-synthetic", "application/json", `{"id":"model-synthetic","object":"model","owned_by":"synthetic-owner"}`, http.StatusOK)
		got := runReadableMain(t, server.URL, nil, "--format", "text", "--transform", "id", "models", "retrieve", "model-synthetic")
		assertReadableProcessSuccess(t, got)
		if got.stdout != "model-synthetic\n" {
			t.Fatalf("model extraction included other response fields: %q", got.stdout)
		}
	})
}

func TestMainReadableStreamedImagesSaveWithRedirectedOutput(t *testing.T) {
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(imageBytes.Bytes())
	event := `{"type":"image_generation.completed","created_at":123,"b64_json":"` + encoded + `","future":9007199254740993}`
	server := readableTestServer(t, "POST /images/generations", "text/event-stream", "data: "+event+"\n\n", http.StatusOK)
	for _, format := range []string{"", "JSON"} {
		home := t.TempDir()
		var args []string
		if format != "" {
			args = append(args, "--format", format)
		}
		args = append(args, "images", "generate", "--prompt", "A tiny orange robot", "--stream", "true")
		got := runReadableMain(t, server.URL, []string{"HOME=" + home, "USERPROFILE=" + home}, args...)
		assertReadableProcessSuccess(t, got)
		if format != "" {
			assertReadableJSONValues(t, got.stdout, event)
			if _, err := os.Stat(filepath.Join(home, "Downloads")); !os.IsNotExist(err) {
				t.Errorf("explicit JSON stream created a download folder: %v", err)
			}
			continue
		}
		assertReadableSuccess(t, got, "tiny-orange-robot.png")
		saved, err := os.ReadFile(filepath.Join(home, "Downloads", "gpt-images", "tiny-orange-robot.png"))
		if err != nil || !bytes.Equal(saved, imageBytes.Bytes()) {
			t.Fatalf("streamed final image was not saved intact: %v", err)
		}
	}
}

func TestMainReadablePreservesBinaryAndEmptyResponses(t *testing.T) {
	const binary = "\x00\x01\x02synthetic download\xff"
	server := readableTestServer(t, "GET /files/file_synthetic/content", "application/octet-stream", binary, http.StatusOK)
	for _, flags := range [][]string{nil, {"--output", "-"}} {
		args := append([]string{"files", "content", "file_synthetic"}, flags...)
		got := runReadableMain(t, server.URL, nil, args...)
		assertReadableProcessSuccess(t, got)
		if got.stdout != binary {
			t.Fatalf("download bytes changed: got %q; want %q", got.stdout, binary)
		}
	}
	server = readableTestServer(t, "DELETE /responses/resp_synthetic", "", "", http.StatusNoContent)
	got := runReadableMain(t, server.URL, nil, "responses", "delete", "resp_synthetic")
	assertReadableProcessSuccess(t, got)
	if got.stdout != "Deleted response \"resp_synthetic\".\n" {
		t.Fatalf("missing readable deletion confirmation: %q", got.stdout)
	}
}

func TestMainReadableAudioTranslationTextFormats(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic.wav")
	if err := os.WriteFile(path, []byte("synthetic audio upload"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ format, contentType, body string }{
		{"text", "text/plain; charset=utf-8", "A synthetic translation.\n"},
		{"srt", "application/x-subrip", "1\n00:00:00,000 --> 00:00:01,000\nA synthetic translation.\n\n"},
		{"vtt", "text/vtt", "WEBVTT\n\n00:00.000 --> 00:01.000\nA synthetic translation.\n\n"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			server := readableTestServer(t, "POST /audio/translations", tc.contentType, tc.body, http.StatusOK)
			for _, format := range []string{"", "auto", "json", "raw"} {
				name := format
				if name == "" {
					name = "default"
				}
				t.Run(name, func(t *testing.T) {
					var args []string
					if format != "" {
						args = append(args, "--format", format)
					}
					args = append(args, "audio:translations", "create", "--file", path, "--model", "whisper-1", "--response-format", tc.format)
					got := runReadableMain(t, server.URL, nil, args...)
					assertReadableProcessSuccess(t, got)
					if format == "json" {
						encoded, err := json.Marshal(tc.body)
						if err != nil {
							t.Fatal(err)
						}
						assertReadableJSONValues(t, got.stdout, string(encoded))
					} else if got.stdout != tc.body {
						t.Fatalf("text response changed: got %q; want %q", got.stdout, tc.body)
					}
				})
			}
		})
	}
}

func TestMainReadableAudioRawPreservesBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic.wav")
	if err := os.WriteFile(path, []byte("synthetic audio upload"), 0o600); err != nil {
		t.Fatal(err)
	}
	const body = "A synthetic\ttranslation.\r\n\x1b[31mOriginal control bytes\x1b[0m"
	server := readableTestServer(t, "POST /audio/translations", "text/plain", body, http.StatusOK)
	args := []string{"audio:translations", "create", "--file", path, "--model", "whisper-1", "--response-format", "text"}
	got := runReadableMain(t, server.URL, nil, args...)
	assertReadableProcessSuccess(t, got)
	if strings.ContainsAny(got.stdout, "\x1b\r") || !strings.Contains(got.stdout, "Original control bytes") {
		t.Fatalf("readable audio did not safely retain the text: %q", got.stdout)
	}
	got = runReadableMain(t, server.URL, nil, append([]string{"--format", "raw"}, args...)...)
	assertReadableProcessSuccess(t, got)
	if got.stdout != body {
		t.Fatalf("explicit raw audio text changed bytes or appended a newline: got %q; want %q", got.stdout, body)
	}
}

func TestMainReadableUnsuccessfulStreams(t *testing.T) {
	const delta = `{"type":"response.output_text.delta","delta":"Partial synthetic answer.","sequence_number":1,"item_id":"msg_synthetic","output_index":0,"content_index":0}`
	for _, tc := range []struct{ name, event string }{
		{"failed", `{"type":"response.failed","sequence_number":2,"response":{"id":"resp_synthetic","object":"response","status":"failed","output":[],"error":{"code":"server_error","message":"Synthetic failure detail"}},"future":9007199254740993}`},
		{"incomplete", `{"type":"response.incomplete","sequence_number":2,"response":{"id":"resp_synthetic","object":"response","status":"incomplete","output":[],"incomplete_details":{"reason":"max_output_tokens"}},"future":9007199254740993}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := readableTestServer(t, "POST /responses", "text/event-stream", "data: "+delta+"\n\ndata: "+tc.event+"\n\n", http.StatusOK)
			args := []string{"responses", "create", "--model", "model-synthetic", "--input", "synthetic question", "--stream", "true"}
			got := runReadableMain(t, server.URL, nil, args...)
			if got.code == 0 || strings.TrimSpace(got.stderr) == "" {
				t.Fatalf("unsuccessful stream must fail and explain on stderr: %+v", got)
			}
			if !strings.Contains(got.stdout, "Partial synthetic answer.") {
				t.Fatalf("stream failure lost partial output: %+v", got)
			}
			if strings.ContainsAny(got.stdout+got.stderr, "\x1b\x00") || strings.Contains(got.stdout, `"type":`) {
				t.Errorf("default failed stream exposed raw JSON/control sequences: %+v", got)
			}
			for _, format := range []string{"json", "JSON"} {
				got := runReadableMain(t, server.URL, nil, append([]string{"--format", format}, args...)...)
				if got.code == 0 || strings.TrimSpace(got.stderr) == "" {
					t.Fatalf("unsuccessful stream must fail even with explicit JSON: %+v", got)
				}
				assertReadableJSONValues(t, got.stdout, delta, tc.event)
			}
		})
	}
}

func TestMainReadableSpeechSSE(t *testing.T) {
	const audio = "c3ludGhldGljLWF1ZGlv"
	const first = `{"type":"speech.audio.delta","audio":"` + audio + `","future":9007199254740993}`
	const last = `{"type":"speech.audio.done","usage":{"input_tokens":1,"output_tokens":2}}`
	const wire = ": synthetic heartbeat\r\nevent: speech.audio.delta\r\ndata: " + first + "\r\n\r\nevent: speech.audio.done\r\ndata: " + last + "\r\n\r\ndata: [DONE]\r\n\r\n"
	server := readableTestServer(t, "POST /audio/speech", "text/event-stream; charset=utf-8", wire, http.StatusOK)
	args := []string{"audio:speech", "create", "--input", "Synthetic speech", "--model", "gpt-4o-mini-tts", "--voice", "alloy", "--stream-format", "sse"}
	for _, flags := range [][]string{nil, {"--format", "auto"}, {"--format", "text"}} {
		got := runReadableMain(t, server.URL, nil, append(append([]string{}, flags...), args...)...)
		assertReadableSuccess(t, got, "speech.audio.delta", "speech.audio.done", "--format json", "9007199254740993")
		if strings.Contains(got.stdout, audio) || strings.Contains(got.stdout, "data:") {
			t.Fatalf("readable speech emitted encoded audio or SSE framing: %q", got.stdout)
		}
	}
	for _, format := range []string{"json", "JSON", "jsonl"} {
		got := runReadableMain(t, server.URL, nil, append([]string{"--format", format}, args...)...)
		assertReadableProcessSuccess(t, got)
		assertReadableJSONValues(t, got.stdout, first, last)
	}
	for _, flags := range [][]string{{"--format", "raw"}, {"--raw-output"}} {
		got := runReadableMain(t, server.URL, nil, append(append([]string{}, flags...), args...)...)
		assertReadableProcessSuccess(t, got)
		if got.stdout != wire {
			t.Fatalf("explicit raw speech changed SSE bytes: %q", got.stdout)
		}
	}
	got := runReadableMain(t, server.URL, nil, append([]string{"--transform", "type", "--raw-output"}, args...)...)
	assertReadableProcessSuccess(t, got)
	if got.stdout != "speech.audio.delta\nspeech.audio.done\n" {
		t.Fatalf("speech event extraction changed: %q", got.stdout)
	}
	got = runReadableMain(t, server.URL, nil, append(append([]string{"--format", "json"}, args...), "--output", "-")...)
	assertReadableProcessSuccess(t, got)
	if got.stdout != wire {
		t.Fatalf("explicit stdout destination changed SSE bytes: %q", got.stdout)
	}
	path := filepath.Join(t.TempDir(), "speech.sse")
	got = runReadableMain(t, server.URL, nil, append(append([]string{"--format", "json"}, args...), "--output", path)...)
	assertReadableSuccess(t, got, path)
	saved, err := os.ReadFile(path)
	if err != nil || string(saved) != wire {
		t.Fatalf("explicit speech output file changed bytes: %v", err)
	}
}

func TestMainReadableSpeechPreservesBinaryResponses(t *testing.T) {
	const body = "ID3\x00synthetic audio bytes\xff"
	server := readableTestServer(t, "POST /audio/speech", "audio/mpeg", body, http.StatusOK)
	args := []string{"audio:speech", "create", "--input", "Synthetic speech", "--model", "gpt-4o-mini-tts", "--voice", "alloy"}
	for _, flags := range [][]string{nil, {"--format", "json"}, {"--format", "text"}} {
		got := runReadableMain(t, server.URL, nil, append(append([]string{}, flags...), args...)...)
		assertReadableProcessSuccess(t, got)
		if got.stdout != body {
			t.Fatalf("binary speech response changed: %q", got.stdout)
		}
	}
}

func readableTestServer(t *testing.T, route, contentType, body string, status int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Method + " " + r.URL.Path; got != route {
			t.Errorf("request = %q; want %q", got, route)
			http.Error(w, "unexpected synthetic request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("x-should-retry", "false")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

func runReadableMain(t *testing.T, baseURL string, env []string, args ...string) mainDispatchResult {
	t.Helper()
	home := t.TempDir()
	environment := append([]string{
		"OPENAI_API_KEY=synthetic-readable-key", "HOME=" + home, "USERPROFILE=" + home,
		"XDG_CONFIG_HOME=" + home, "XDG_CACHE_HOME=" + home,
		"HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=127.0.0.1,localhost",
		"FORCE_COLOR=0", "NO_COLOR=1", "TERM=dumb", "CI=1",
	}, env...)
	argv := append([]string{"openai", "--base-url", baseURL}, args...)
	return runMainDispatchWithEnv(t, "bash", environment, argv...)
}

func assertReadableProcessSuccess(t *testing.T, got mainDispatchResult) {
	t.Helper()
	if got.code != 0 || got.stderr != "" {
		t.Fatalf("command failed or emitted diagnostics: %+v", got)
	}
}

func assertReadableSuccess(t *testing.T, got mainDispatchResult, content ...string) {
	t.Helper()
	assertReadableProcessSuccess(t, got)
	for _, want := range content {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("readable output missing %q: %q", want, got.stdout)
		}
	}
	if strings.ContainsAny(got.stdout, "\x1b\x00") || json.Valid([]byte(got.stdout)) || strings.HasPrefix(strings.TrimSpace(got.stdout), "{") {
		t.Errorf("default output is JSON or contains terminal controls: %q", got.stdout)
	}
}

func assertReadableJSONValues(t *testing.T, output string, expected ...string) {
	t.Helper()
	decode := func(value string) []any {
		decoder := json.NewDecoder(strings.NewReader(value))
		decoder.UseNumber()
		var values []any
		for {
			var item any
			if err := decoder.Decode(&item); err == io.EOF {
				return values
			} else if err != nil {
				t.Fatalf("invalid machine output: %v; %q", err, value)
			}
			values = append(values, item)
		}
	}
	if got, want := decode(output), decode(strings.Join(expected, "\n")); !reflect.DeepEqual(got, want) {
		t.Fatalf("machine output changed payloads: got %s; want %s", output, strings.Join(expected, "\n"))
	}
}
