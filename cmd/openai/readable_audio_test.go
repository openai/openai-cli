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

func readableAudioArgs(t *testing.T, command string, extra ...string) []string {
	t.Helper()
	args := []string{command, "create", "--model", "fake-audio-model"}
	if command == "audio:speech" {
		args = append(args, "--input", "Synthetic speech", "--voice", "alloy")
	} else {
		path := filepath.Join(t.TempDir(), "synthetic.wav")
		if err := os.WriteFile(path, []byte("synthetic audio upload"), 0o600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "--file", path)
	}
	return append(args, extra...)
}

func readableAudioServer(t *testing.T, command, contentType, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/"+strings.ReplaceAll(command, ":", "/") {
			t.Errorf("unexpected audio request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected synthetic request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", contentType)
		io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

func assertReadableAudioJSON(t *testing.T, output string, events ...string) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.UseNumber()
	for i, event := range events {
		var actual, want any
		if err := decoder.Decode(&actual); err != nil {
			t.Fatalf("decode audio result %d: %v", i, err)
		}
		original := json.NewDecoder(strings.NewReader(event))
		original.UseNumber()
		if err := original.Decode(&want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actual, want) {
			t.Fatalf("audio result %d changed: got %v, want %v", i, actual, want)
		}
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("unexpected trailing audio output: %v", err)
	}
}

func TestMainReadableAudioJSONPreservesTextAndMetadata(t *testing.T) {
	const body = `{"text":"A synthetic transcript.","language":"english","duration":2.5,"segments":[{"id":"seg_synthetic","speaker":"speaker_a","start":0,"end":2.5,"text":"Segment detail","tokens":[100,200]}],"words":[{"word":"synthetic","start":0.5,"end":1.5}],"logprobs":[{"token":"transcript","logprob":-0.25,"bytes":[65]}],"usage":{"type":"tokens","input_tokens":7,"output_tokens":3,"total_tokens":10},"future":{"count":9007199254740993}}`
	for _, command := range []string{"audio:transcriptions", "audio:translations"} {
		t.Run(command, func(t *testing.T) {
			server := readableAudioServer(t, command, "application/json", body)
			args := readableAudioArgs(t, command)
			for _, flags := range [][]string{nil, {"--format", "text"}} {
				got := runReadableCommand(t, server, append(flags, args...)...)
				if got.code != 0 || got.stderr != "" || !strings.HasPrefix(got.stdout, "A synthetic transcript.\n") || strings.Count(got.stdout, "A synthetic transcript.") != 1 {
					t.Fatalf("readable transcript changed: %+v", got)
				}
				for _, detail := range []string{"english", "2.5", "seg_synthetic", "speaker_a", "Segment detail", "100", "200", "Words:", "0.5", "1.5", "Logprobs:", "-0.25", "65", "Input tokens: 7", "Output tokens: 3", "Total tokens: 10", "9007199254740993"} {
					if !strings.Contains(got.stdout, detail) {
						t.Fatalf("audio metadata %q lost: %q", detail, got.stdout)
					}
				}
			}
			for _, format := range []string{"json", "jsonl", "raw"} {
				got := runReadableCommand(t, server, append([]string{"--format", format}, args...)...)
				if got.code != 0 || got.stderr != "" {
					t.Fatalf("explicit audio format failed: %+v", got)
				}
				assertReadableAudioJSON(t, got.stdout, body)
				if format == "raw" && got.stdout != body+"\n" {
					t.Fatalf("raw JSON bytes changed: %q", got.stdout)
				}
			}
			got := runReadableCommand(t, server, append([]string{"--transform", "text", "--raw-output"}, args...)...)
			if got.code != 0 || got.stderr != "" || got.stdout != "A synthetic transcript.\n" {
				t.Fatalf("explicit audio extraction changed: %+v", got)
			}
		})
	}
}

func TestMainReadableAudioTextAndSubtitles(t *testing.T) {
	for _, command := range []string{"audio:transcriptions", "audio:translations"} {
		for _, tc := range []struct{ format, contentType, body string }{
			{"text", "text/plain; charset=utf-8", "A synthetic transcript.\n"},
			{"srt", "application/x-subrip", "1\n00:00:00,000 --> 00:00:01,000\nA synthetic transcript.\n\n"},
			{"vtt", "text/vtt", "WEBVTT\n\n00:00.000 --> 00:01.000\nA synthetic transcript.\n\n"},
		} {
			t.Run(command+"/"+tc.format, func(t *testing.T) {
				server := readableAudioServer(t, command, tc.contentType, tc.body)
				args := readableAudioArgs(t, command, "--response-format", tc.format)
				for _, format := range []string{"auto", "json", "raw"} {
					got := runReadableCommand(t, server, append([]string{"--format", format}, args...)...)
					if got.code != 0 || got.stderr != "" {
						t.Fatalf("subtitle response failed: %+v", got)
					}
					if format == "json" {
						encoded, _ := json.Marshal(tc.body)
						assertReadableAudioJSON(t, got.stdout, string(encoded))
					} else if got.stdout != tc.body {
						t.Fatalf("subtitle bytes changed: got %q, want %q", got.stdout, tc.body)
					}
				}
			})
		}
	}
	const body = "Synthetic\ttext.\r\n\x1b[31mOriginal controls\x1b[0m"
	server := readableAudioServer(t, "audio:translations", "text/plain", body)
	args := readableAudioArgs(t, "audio:translations", "--response-format", "text")
	got := runReadableCommand(t, server, args...)
	if got.code != 0 || got.stderr != "" || strings.ContainsAny(got.stdout, "\x1b\r") || !strings.Contains(got.stdout, "Original controls") {
		t.Fatalf("readable audio text lost content or emitted controls: %+v", got)
	}
	got = runReadableCommand(t, server, append([]string{"--format", "raw"}, args...)...)
	if got.code != 0 || got.stderr != "" || got.stdout != body {
		t.Fatalf("raw audio text changed original bytes: %+v", got)
	}
}

func TestMainReadableAudioTranscriptionStreamsBeforeDone(t *testing.T) {
	release := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		defer r.MultipartForm.RemoveAll()
		if r.URL.Path != "/audio/transcriptions" || r.FormValue("model") != "fake-audio-model" || r.FormValue("stream") != "true" {
			t.Errorf("transcription request changed: path=%s model=%q stream=%q", r.URL.Path, r.FormValue("model"), r.FormValue("stream"))
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			return
		}
		defer file.Close()
		upload, err := io.ReadAll(file)
		if err != nil || string(upload) != "synthetic audio upload" {
			t.Errorf("audio upload changed: %q, %v", upload, err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeStreamingTextEvent(w, `{"type":"transcript.text.delta","delta":"Hello ☀"}`)
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		writeStreamingTextEvent(w, `{"type":"transcript.text.done","text":"Hello ☀ world","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	child, stdout, stderr, ctx := startStreamingTextCommand(t, server, readableAudioArgs(t, "audio:transcriptions", "--stream=true")...)
	readStreamingTextPrefix(t, ctx, stdout, "Hello ☀")
	release <- struct{}{}
	rest, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil || stderr.Len() != 0 {
		t.Fatalf("transcription stream failed: %v, stderr=%q", err, stderr.String())
	}
	if !strings.HasPrefix(string(rest), " world\n") || strings.Contains(string(rest), "Hello") || !strings.Contains(string(rest), "Total tokens: 5") {
		t.Fatalf("transcription terminal snapshot duplicated text or lost usage: %q", rest)
	}
}

func TestMainReadableAudioTranscriptionKeepsEventsAndMetadata(t *testing.T) {
	events := []string{
		`{"type":"transcript.text.delta","delta":"Segment text","segment_id":"seg_a","logprobs":[{"token":"Segment","logprob":-0.3,"bytes":[83]}]}`,
		`{"type":"transcript.text.segment","id":"seg_a","speaker":"speaker_a","start":0,"end":1.5,"text":"Segment text"}`,
		`{"type":"transcript.future_event","future":{"count":9007199254740993}}`,
		`{"type":"transcript.text.done","text":"Segment text","languages":[{"language":"en","probability":0.99}],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`,
	}
	server := streamingTextServer(t, events)
	args := readableAudioArgs(t, "audio:transcriptions", "--stream=true", "--response-format", "diarized_json")
	got := runReadableCommand(t, server, args...)
	if got.code != 0 || got.stderr != "" {
		t.Fatalf("diarized transcript failed: %+v", got)
	}
	for _, detail := range []string{"Segment text", "seg_a", "speaker_a", "1.5", "-0.3", "83", "transcript.future_event", "9007199254740993", "Languages:", "en", "0.99", "Total tokens: 5"} {
		if !strings.Contains(got.stdout, detail) {
			t.Fatalf("transcription event detail %q lost: %q", detail, got.stdout)
		}
	}
	for _, format := range []string{"raw", "json", "jsonl"} {
		got := runReadableCommand(t, server, append([]string{"--format", format}, args...)...)
		if got.code != 0 || got.stderr != "" {
			t.Fatalf("explicit transcription stream failed: %+v", got)
		}
		assertReadableAudioJSON(t, got.stdout, events...)
		if format == "raw" && got.stdout != strings.Join(events, "\n")+"\n" {
			t.Fatalf("raw transcription event bytes changed: %q", got.stdout)
		}
	}
}

func TestMainReadableAudioStreamFailuresKeepPartialOutput(t *testing.T) {
	for _, command := range []string{"audio:transcriptions", "audio:speech"} {
		first := `{"type":"transcript.text.delta","delta":"Partial transcript"}`
		extra := []string{"--stream=true"}
		partial := "Partial transcript"
		if command == "audio:speech" {
			first = `{"type":"speech.audio.delta","audio":"c3ludGhldGlj","future":"Partial speech"}`
			extra = []string{"--stream-format", "sse"}
			partial = "Partial speech"
		}
		for _, tc := range []struct{ name, last string }{
			{"early EOF", ""},
			{"error", `{"type":"error","error":{"message":"synthetic-private-detail https://secret.invalid/?token=fake\u001b[2J"}}`},
			{"malformed", `{"type":"synthetic-private-detail","delta":`},
		} {
			for _, format := range []string{"text", "jsonl"} {
				t.Run(command+"/"+tc.name+"/"+format, func(t *testing.T) {
					wire := "data: " + first + "\n\n"
					if tc.last != "" {
						wire += "data: " + tc.last + "\n\n"
					}
					server := readableAudioServer(t, command, "text/event-stream", wire)
					// Explicit JSON API errors preserve the parent's original API
					// payload contract. Select safe text diagnostics independently.
					args := append([]string{"--format", format, "--format-error", "text"}, readableAudioArgs(t, command, extra...)...)
					got := runReadableCommand(t, server, args...)
					if got.code == 0 || got.stderr == "" || !strings.Contains(got.stdout, partial) {
						t.Fatalf("audio failure lost partial output or success status: %+v", got)
					}
					for _, private := range []string{"synthetic-private-detail", "secret.invalid", "token=", "\x1b"} {
						if strings.Contains(got.stderr, private) {
							t.Fatalf("audio diagnostic exposed API data: %q", got.stderr)
						}
					}
				})
			}
		}
	}
}

func TestMainReadableAudioSpeechFormatsAndDownloads(t *testing.T) {
	const audio = "c3ludGhldGljLWF1ZGlv"
	const first = `{"type":"speech.audio.delta","audio":"` + audio + `","future":9007199254740993}`
	const last = `{"type":"speech.audio.done","usage":{"input_tokens":1,"output_tokens":2}}`
	const wire = ": synthetic heartbeat\r\nevent: speech.audio.delta\r\ndata: " + first + "\r\n\r\nevent: speech.audio.done\r\ndata: " + last + "\r\n\r\ndata: [DONE]\r\n\r\n"
	server := readableAudioServer(t, "audio:speech", "text/event-stream; charset=utf-8", wire)
	args := readableAudioArgs(t, "audio:speech", "--stream-format", "sse")
	got := runReadableCommand(t, server, args...)
	if got.code != 0 || got.stderr != "" || strings.Contains(got.stdout, audio) || strings.Contains(got.stdout, "data:") {
		t.Fatalf("readable speech stream failed or emitted encoded audio: %+v", got)
	}
	for _, detail := range []string{"speech.audio.delta", "speech.audio.done", "--format json", "9007199254740993", "Input tokens: 1", "Output tokens: 2"} {
		if !strings.Contains(got.stdout, detail) {
			t.Fatalf("speech detail %q lost: %q", detail, got.stdout)
		}
	}
	for _, format := range []string{"json", "jsonl"} {
		got := runReadableCommand(t, server, append([]string{"--format", format}, args...)...)
		if got.code != 0 || got.stderr != "" {
			t.Fatalf("explicit speech events failed: %+v", got)
		}
		assertReadableAudioJSON(t, got.stdout, first, last)
	}
	for _, flags := range [][]string{{"--format", "raw"}, {"--raw-output"}} {
		got := runReadableCommand(t, server, append(flags, args...)...)
		if got.code != 0 || got.stderr != "" || got.stdout != wire {
			t.Fatalf("raw speech SSE bytes changed: %+v", got)
		}
	}
	got = runReadableCommand(t, server, append([]string{"--transform", "type", "--raw-output"}, args...)...)
	if got.code != 0 || got.stderr != "" || got.stdout != "speech.audio.delta\nspeech.audio.done\n" {
		t.Fatalf("speech event extraction changed: %+v", got)
	}
	got = runReadableCommand(t, server, append(append([]string{"--format", "json"}, args...), "--output", "-")...)
	if got.code != 0 || got.stderr != "" || got.stdout != wire {
		t.Fatalf("explicit stdout changed speech SSE bytes: %+v", got)
	}
	path := filepath.Join(t.TempDir(), "speech.sse")
	got = runReadableCommand(t, server, append(append([]string{"--format", "json"}, args...), "--output", path)...)
	saved, err := os.ReadFile(path)
	if got.code != 0 || got.stderr != "" || err != nil || string(saved) != wire || !strings.Contains(got.stdout, path) {
		t.Fatalf("speech output file changed bytes or failed: %+v, error=%v", got, err)
	}
}

func TestMainReadableAudioSpeechPreservesBinary(t *testing.T) {
	const body = "ID3\x00synthetic audio bytes\xff\x1b\r\n"
	server := readableAudioServer(t, "audio:speech", "audio/mpeg", body)
	args := readableAudioArgs(t, "audio:speech")
	for _, flags := range [][]string{nil, {"--format", "json"}, {"--format", "text"}} {
		got := runReadableCommand(t, server, append(flags, args...)...)
		if got.code != 0 || got.stderr != "" || got.stdout != body {
			t.Fatalf("binary audio changed: %+v", got)
		}
	}
	path := filepath.Join(t.TempDir(), "speech.mp3")
	got := runReadableCommand(t, server, append(args, "--output", path)...)
	saved, err := os.ReadFile(path)
	if got.code != 0 || got.stderr != "" || err != nil || string(saved) != body {
		t.Fatalf("binary audio download changed: %+v, error=%v", got, err)
	}
}

func TestMainReadableAudioLargePayloads(t *testing.T) {
	// These high-memory probes intentionally cross the former 32 MiB capture
	// limits. Run sequentially and do not reduce them to accommodate a new cap.
	text := strings.Repeat("x", (32<<20)+1)
	t.Run("JSON", func(t *testing.T) {
		server := readableAudioServer(t, "audio:transcriptions", "application/json", `{"text":"`+text+`","duration":2.5,"future":"after large transcript"}`)
		got := runReadableCommand(t, server, readableAudioArgs(t, "audio:transcriptions")...)
		if got.code != 0 || got.stderr != "" || !strings.HasPrefix(got.stdout, text+"\n") || !strings.Contains(got.stdout[len(text):], "after large transcript") {
			t.Fatalf("large transcript changed: code=%d, bytes=%d, stderr=%q", got.code, len(got.stdout), got.stderr)
		}
	})
	t.Run("transcription SSE", func(t *testing.T) {
		// Leave envelope space below the SDK's longstanding 32 MiB SSE line cap.
		transcript := text[:(32<<20)-1024]
		server := streamingTextServer(t, []string{`{"type":"transcript.text.delta","delta":"` + transcript + `"}`, `{"type":"transcript.text.done","text":"` + transcript + `"}`})
		got := runReadableCommand(t, server, readableAudioArgs(t, "audio:transcriptions", "--stream=true")...)
		if got.code != 0 || got.stderr != "" || got.stdout != transcript+"\n" {
			t.Fatalf("large transcription event changed: code=%d, bytes=%d, stderr=%q", got.code, len(got.stdout), got.stderr)
		}
	})
	t.Run("speech SSE", func(t *testing.T) {
		// Speech was an uncapped binary response, so its new parser must accept
		// a single line larger than the SDK's separate transcription limit.
		audio := strings.Repeat("YWFh", (8<<20)+1)
		first := `{"type":"speech.audio.delta","audio":"` + audio + `","future":"after large audio"}`
		last := `{"type":"speech.audio.done","usage":{"input_tokens":1,"output_tokens":2}}`
		server := readableAudioServer(t, "audio:speech", "text/event-stream", "data: "+first+"\n\ndata: "+last+"\n\n")
		args := readableAudioArgs(t, "audio:speech", "--stream-format", "sse")
		got := runReadableCommand(t, server, args...)
		if got.code != 0 || got.stderr != "" || strings.Contains(got.stdout, "YWFh") || !strings.Contains(got.stdout, "after large audio") || !strings.Contains(got.stdout, "Output tokens: 2") {
			t.Fatalf("large readable speech event changed: code=%d, bytes=%d, stderr=%q", got.code, len(got.stdout), got.stderr)
		}
		got = runReadableCommand(t, server, append([]string{"--format", "jsonl"}, args...)...)
		if got.code != 0 || got.stderr != "" || got.stdout != first+"\n"+last+"\n" {
			t.Fatalf("large explicit speech event lost bytes: code=%d, bytes=%d, stderr=%q", got.code, len(got.stdout), got.stderr)
		}
	})
}
