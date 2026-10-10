package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMainTranscriptTimestampsReadableOrderAndDetails(t *testing.T) {
	const body = `{"text":"First. Second. Third.","segments":[{"id":"seg_hour","speaker":"A","start":3600.125,"end":3605.2,"text":"First. "},{"id":"seg_overlap_a","speaker":"B","start":0.125,"end":1.5,"text":"Second. ","confidence":0.875},{"id":"seg_overlap_b","speaker":"李","start":0.875,"end":2,"text":"Third.","future":{"count":9007199254740993}}],"usage":{"total_tokens":7}}`
	for _, tc := range []struct {
		name, endpoint string
		command        []string
	}{
		{"transcribe shortcut", "audio:transcriptions", []string{"audio", "transcribe"}},
		{"transcribe generated", "audio:transcriptions", []string{"audio:transcriptions", "create"}},
		{"translate shortcut", "audio:translations", []string{"audio", "translate"}},
		{"translate generated", "audio:translations", []string{"audio:translations", "create"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := readableAudioServer(t, tc.endpoint, "application/json", body)
			args := append(tc.command, readableAudioArgs(t, tc.endpoint)[2:]...)
			for _, format := range []string{"auto", "text"} {
				t.Run(format, func(t *testing.T) {
					got := runReadableCommand(t, server, append([]string{"--format", format}, args...)...)
					if got.code != 0 || got.stderr != "" {
						t.Fatalf("readable transcript failed: %+v", got)
					}
					previous := -1
					for _, line := range []string{
						"[01:00:00.125–01:00:05.200] A: First.",
						"[00:00.125–00:01.500] B: Second.",
						"[00:00.875–00:02.000] 李: Third.",
					} {
						position := strings.Index(got.stdout, line)
						if position <= previous {
							t.Fatalf("segment missing or reordered: %q in %q", line, got.stdout)
						}
						previous = position
					}
					for _, detail := range []string{"seg_hour", "seg_overlap_a", "seg_overlap_b", "Confidence: 0.875", "9007199254740993", "Total tokens: 7"} {
						if !strings.Contains(got.stdout, detail) {
							t.Fatalf("transcript detail %q lost: %q", detail, got.stdout)
						}
					}
					for _, text := range []string{"First.", "Second.", "Third."} {
						if strings.Count(got.stdout, text) != 1 {
							t.Fatalf("transcript text duplicated: %q", got.stdout)
						}
					}
				})
			}
		})
	}
}

func TestMainTranscriptTimestampsDiarizedRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meeting.wav")
	if err := os.WriteFile(path, []byte("synthetic audio upload"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/audio/transcriptions" {
			t.Errorf("diarized request changed: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected synthetic request", http.StatusBadRequest)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			http.Error(w, "invalid synthetic request", http.StatusBadRequest)
			return
		}
		defer r.MultipartForm.RemoveAll()
		for field, want := range map[string]string{
			"model": "gpt-4o-transcribe-diarize", "response_format": "diarized_json", "chunking_strategy": "auto",
		} {
			if got := r.FormValue(field); got != want {
				t.Errorf("diarized request field %s = %q; want %q", field, got, want)
			}
		}
		if r.FormValue("stream") == "true" {
			t.Error("finite transcript request enabled streaming")
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			http.Error(w, "missing synthetic file", http.StatusBadRequest)
			return
		}
		defer file.Close()
		upload, err := io.ReadAll(file)
		if err != nil || string(upload) != "synthetic audio upload" || header.Filename != "meeting.wav" {
			t.Errorf("diarized upload changed: filename=%q body=%q err=%v", header.Filename, upload, err)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"text":"Thanks for calling.","segments":[{"text":"Thanks for calling.","speaker":"A","start":0,"end":5.2}]}`)
	}))
	t.Cleanup(server.Close)
	got := runReadableCommand(t, server, "audio", "transcribe", "--file", path, "--model", "gpt-4o-transcribe-diarize", "--response-format", "diarized_json", "--chunking-strategy", "auto")
	if got.code != 0 || got.stderr != "" || got.stdout != "[00:00.000–00:05.200] A: Thanks for calling.\n" {
		t.Fatalf("documented diarized command changed: %+v", got)
	}
}

func TestMainTranscriptTimestampsFallbacks(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       []string
		absent     []string
	}{
		{
			name: "missing speaker and rounded times",
			body: `{"text":"Fractional.","segments":[{"text":"Fractional.","start":0.1234,"end":1.2346}]}`,
			want: []string{"[00:00.123–00:01.235] Fractional.", "0.1234", "1.2346"},
		},
		{
			name: "precise decimal timestamps",
			body: `{"text":"Exact.","segments":[{"text":"Exact.","start":0.0005,"end":1.00000000000000001}]}`,
			want: []string{"[00:00.001–00:01.000] Exact.", "Start: 0.0005", "End: 1.00000000000000001"},
		},
		{
			name: "reversed close decimal timestamps",
			body: `{"text":"Reversed.","segments":[{"text":"Reversed.","start":1.00000000000000002,"end":1.00000000000000001}]}`,
			want: []string{"Reversed.", "Start: 1.00000000000000002", "End: 1.00000000000000001"}, absent: []string{"[00:"},
		},
		{
			name: "boundary controls remain visible",
			body: `{"text":"\rHello 世界\u2028","segments":[{"text":"\rHello 世界\u2028","start":0,"end":1}]}`,
			want: []string{`[00:00.000–00:01.000] \rHello 世界\u2028`}, absent: []string{"\r", "\u2028"},
		},
		{
			name: "missing time",
			body: `{"text":"Untimed.","segments":[{"speaker":"A","text":"Untimed.","start":0.25}]}`,
			want: []string{"A: Untimed.", "0.25"}, absent: []string{"[00:"},
		},
		{
			name: "reversed time",
			body: `{"text":"Reversed.","segments":[{"speaker":"B","text":"Reversed.","start":9,"end":2}]}`,
			want: []string{"B: Reversed.", "Start: 9", "End: 2"}, absent: []string{"[00:"},
		},
		{
			name: "malformed time and speaker",
			body: `{"text":"Malformed.","segments":[{"speaker":{"future":"speaker value"},"text":"Malformed.","start":"later","end":null}]}`,
			want: []string{"Malformed.", "speaker value", "later", "End: (null)"}, absent: []string{"[00:"},
		},
		{
			name: "incomplete segment text",
			body: `{"text":"Complete transcript.","segments":[{"speaker":"A","text":"Complete","start":0,"end":1},{"speaker":"B","text":7,"start":1,"end":2}]}`,
			want: []string{"Complete transcript.\n", "Text: 7", "Speaker: B"}, absent: []string{"[00:"},
		},
		{
			name: "unfamiliar segment shape",
			body: `{"text":"Complete transcript.","segments":[null,{"future":"segment value"}]}`,
			want: []string{"Complete transcript.\n", "segment value", "null"}, absent: []string{"[00:"},
		},
		{
			name: "aggregate contains additional text",
			body: `{"text":"Complete transcript with extra words.","segments":[{"speaker":"A","text":"Partial segment.","start":0,"end":1}]}`,
			want: []string{"Complete transcript with extra words.\n", "Segments:", "[00:00.000–00:01.000] A: Partial segment."},
		},
		{
			name: "empty segments preserve metadata",
			body: `{"text":"","segments":[],"future":"empty detail"}`,
			want: []string{"empty detail"}, absent: []string{"[00:"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := readableAudioServer(t, "audio:transcriptions", "application/json", tc.body)
			got := runReadableCommand(t, server, readableAudioArgs(t, "audio:transcriptions")...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("fallback transcript failed: %+v", got)
			}
			for _, detail := range tc.want {
				if !strings.Contains(got.stdout, detail) {
					t.Fatalf("fallback transcript lost %q: %q", detail, got.stdout)
				}
			}
			for _, unwanted := range tc.absent {
				if strings.Contains(got.stdout, unwanted) {
					t.Fatalf("fallback transcript invented %q: %q", unwanted, got.stdout)
				}
			}
		})
	}
}

func TestMainTranscriptTimestampsExplicitFormats(t *testing.T) {
	const body = `{"text":"Hello 世界","segments":[{"id":"seg_first","speaker":"A","start":0.125,"end":2.75,"text":"Hello 世界","confidence":0.875,"future":{"count":9007199254740993}}],"usage":{"total_tokens":7}}`
	server := readableAudioServer(t, "audio:transcriptions", "application/json", body)
	args := readableAudioArgs(t, "audio:transcriptions", "--response-format", "diarized_json")
	for _, format := range []string{"json", "jsonl", "raw"} {
		t.Run(format, func(t *testing.T) {
			got := runReadableCommand(t, server, append([]string{"--format", format}, args...)...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("explicit transcript format failed: %+v", got)
			}
			assertReadableAudioJSON(t, got.stdout, body)
			if format == "raw" && got.stdout != body+"\n" {
				t.Fatalf("raw transcript bytes changed: %q", got.stdout)
			}
		})
	}
	for _, tc := range []struct {
		name, path, want string
	}{
		{"full transcript", "text", "Hello 世界\n"},
		{"speaker", "segments.0.speaker", "A\n"},
		{"fractional start", "segments.0.start", "0.125\n"},
		{"unknown integer", "segments.0.future.count", "9007199254740993\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runReadableCommand(t, server, append([]string{"--transform", tc.path, "--raw-output"}, args...)...)
			if got.code != 0 || got.stderr != "" || got.stdout != tc.want {
				t.Fatalf("transcript extraction changed: %+v; want %q", got, tc.want)
			}
		})
	}
}

func TestMainTranscriptTimestampsEscapesControls(t *testing.T) {
	const transcript = "Hello 世界\x1b[2J\r\u202e!"
	const speaker = "A\x1b]52;c;synthetic\a"
	body, err := json.Marshal(map[string]any{
		"text": transcript,
		"segments": []map[string]any{{
			"text": transcript, "speaker": speaker, "start": 0.125, "end": 1.5,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := readableAudioServer(t, "audio:transcriptions", "application/json", string(body))
	args := readableAudioArgs(t, "audio:transcriptions", "--response-format", "diarized_json")
	got := runReadableCommand(t, server, args...)
	if got.code != 0 || got.stderr != "" || strings.ContainsAny(got.stdout, "\x1b\a\r\u202e") {
		t.Fatalf("readable transcript emitted controls or failed: %+v", got)
	}
	for _, detail := range []string{"[00:00.125–00:01.500]", `A\u001b]52;c;synthetic\u0007`, `Hello 世界\u001b[2J\r\u202e!`} {
		if !strings.Contains(got.stdout, detail) {
			t.Fatalf("escaped transcript detail %q lost: %q", detail, got.stdout)
		}
	}
	if strings.Count(got.stdout, "Hello 世界") != 1 {
		t.Fatalf("readable transcript duplicated complete segment text: %q", got.stdout)
	}
	got = runReadableCommand(t, server, append([]string{"--format", "raw"}, args...)...)
	if got.code != 0 || got.stderr != "" || got.stdout != string(body)+"\n" {
		t.Fatalf("raw transcript changed escaped JSON bytes: %+v", got)
	}
}

func TestMainTranscriptTimestampsCancellationClosesResponse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt delivery requires a Unix process")
	}
	for _, format := range []string{"text", "json", "raw"} {
		t.Run(format, func(t *testing.T) {
			ready := make(chan struct{})
			disconnected := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					t.Error(err)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"text":"Incomplete synthetic transcript","segments":[`)
				w.(http.Flusher).Flush()
				close(ready)
				<-r.Context().Done()
				close(disconnected)
			}))
			t.Cleanup(server.Close)
			args := append([]string{"--format", format, "--format-error", "text"}, readableAudioArgs(t, "audio:transcriptions")...)
			child, stdout, stderr, ctx := startStreamingTextCommand(t, server, args...)
			select {
			case <-ready:
			case <-ctx.Done():
				t.Fatal("finite transcript request did not reach its response body")
			}
			if err := child.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			output, readErr := io.ReadAll(stdout)
			waitErr := child.Wait()
			if ctx.Err() != nil {
				t.Fatal("finite transcript cancellation did not finish promptly")
			}
			exit, ok := waitErr.(*exec.ExitError)
			if readErr != nil || !ok || exit.ExitCode() == 0 || len(output) != 0 || stderr.Len() != 0 {
				t.Fatalf("finite cancellation changed: read=%v wait=%v stdout=%q stderr=%q", readErr, waitErr, output, stderr.String())
			}
			select {
			case <-disconnected:
			case <-ctx.Done():
				t.Fatal("finite transcript cancellation retained the HTTP response")
			}
		})
	}
}

func TestMainTranscriptTimestampsLargeText(t *testing.T) {
	// This sequential probe crosses the former 32 MiB capture limit. Do not
	// reduce the payload to accommodate a newly introduced transcript cap.
	transcript := strings.Repeat("x", (32<<20)+1)
	body := `{"text":"` + transcript + `","segments":[{"text":"` + transcript + `","speaker":"A","start":0,"end":1}],"future":"after large segment"}`
	server := readableAudioServer(t, "audio:transcriptions", "application/json", body)
	got := runReadableCommand(t, server, readableAudioArgs(t, "audio:transcriptions")...)
	prefix := "[00:00.000–00:01.000] A: "
	if got.code != 0 || got.stderr != "" || !strings.HasPrefix(got.stdout, prefix) {
		t.Fatalf("large transcript failed: code=%d bytes=%d stderr=%q", got.code, len(got.stdout), got.stderr)
	}
	rest := got.stdout[len(prefix):]
	if !strings.HasPrefix(rest, transcript+"\n") || !strings.Contains(rest[len(transcript):], "after large segment") || strings.Count(rest, transcript) != 1 {
		t.Fatalf("large transcript lost or duplicated text: code=%d bytes=%d stderr=%q", got.code, len(got.stdout), got.stderr)
	}
}
