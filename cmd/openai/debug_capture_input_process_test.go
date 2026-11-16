package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMainDebugCaptureJSONAndYAMLInputPreserveBytes(t *testing.T) {
	hashes := make(chan [32]byte, 2)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var request map[string]any
		if err := json.Unmarshal(body, &request); err != nil || request["model"] != "fake-model" || request["input"] != "synthetic-private-<input>" || request["store"] != false || request["temperature"] != float64(0) {
			t.Errorf("stdin request changed: %q; error=%v", body, err)
		}
		if value, exists := request["previous_response_id"]; !exists || value != nil {
			t.Error("stdin request lost explicit null")
		}
		select {
		case hashes <- sha256.Sum256(body):
		default:
			t.Error("unexpected extra request")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_synthetic","object":"response","status":"completed","output":[]}`)
	}))
	t.Cleanup(server.Close)
	for _, input := range []struct{ name, body string }{
		{"JSON", `{"model":"fake-model","input":"synthetic-private-\u003cinput\u003e","store":false,"temperature":0,"previous_response_id":null}`},
		{"YAML", "model: fake-model\ninput: synthetic-private-<input>\nstore: false\ntemperature: 0\nprevious_response_id: null\n"},
	} {
		t.Run(input.name, func(t *testing.T) {
			env := debugCaptureProcessEnv(t, server)
			args := []string{"openai", "--format", "json", "responses", "create"}
			control := runMainDispatchWithStdin(t, "bash", env, shellFileInput(t, []byte(input.body)), args...)
			capture := append([]string{"openai", "--debug", "--debug-body"}, args[1:]...)
			got := runMainDispatchWithStdin(t, "bash", env, shellFileInput(t, []byte(input.body)), capture...)
			if control.code != 0 || control.stderr != "" || got.code != control.code || got.stdout != control.stdout {
				t.Fatalf("capture changed stdin behavior: control=%+v capture=%+v", control, got)
			}
			if len(hashes) != 2 {
				t.Fatalf("received %d requests, want two", len(hashes))
			}
			controlHash, captureHash := <-hashes, <-hashes
			if captureHash != controlHash {
				t.Fatal("debug capture changed the serialized request")
			}
			for _, safe := range []string{`"store":false`, `"temperature":0`} {
				if !strings.Contains(got.stderr, safe) {
					t.Errorf("capture lost permitted request structure %q: %q", safe, got.stderr)
				}
			}
			assertDebugCapturePrivate(t, got.stderr, "synthetic-private-<input>", "synthetic-private-\\u003cinput\\u003e")
		})
	}
	if requests.Load() != 4 {
		t.Fatalf("received %d requests, want four", requests.Load())
	}
}

func TestMainDebugCaptureMultipartPreservesUploadBytes(t *testing.T) {
	payload := []byte("synthetic-private-upload\x00\xff\n")
	file := filepath.Join(t.TempDir(), "@synthetic-upload.bin")
	if err := os.WriteFile(file, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, stdin := range []bool{false, true} {
		name := "file"
		if stdin {
			name = "stdin"
		}
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				reader, err := r.MultipartReader()
				if err != nil {
					t.Error(err)
					return
				}
				parts := map[string][]byte{}
				for {
					part, err := reader.NextPart()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Error(err)
						return
					}
					parts[part.FormName()], err = io.ReadAll(part)
					part.Close()
					if err != nil {
						t.Error(err)
						return
					}
				}
				if !bytes.Equal(parts["file"], payload) || string(parts["purpose"]) != "user_data" || len(parts) != 2 {
					t.Errorf("multipart values changed: %v", parts)
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"id":"file_synthetic","object":"file","bytes":26,"filename":"synthetic.bin","purpose":"user_data"}`)
			}))
			t.Cleanup(server.Close)
			env := debugCaptureProcessEnv(t, server)
			path := file
			if stdin {
				path = "-"
			}
			args := []string{"openai", "--format", "json", "files", "create", "--file", path, "--purpose", "user_data"}
			run := func(argv []string) mainDispatchResult {
				var input *os.File
				if stdin {
					input = shellFileInput(t, payload)
				}
				return runMainDispatchWithStdin(t, "bash", env, input, argv...)
			}
			control := run(args)
			got := run(append([]string{"openai", "--debug", "--debug-body"}, args[1:]...))
			if control.code != 0 || control.stderr != "" || got.code != control.code || got.stdout != control.stdout || requests.Load() != 2 {
				t.Fatalf("capture changed upload behavior: control=%+v capture=%+v requests=%d", control, got, requests.Load())
			}
			if !strings.Contains(got.stderr, "request body (redacted;") || !strings.Contains(got.stderr, "<omitted: non-JSON body>") {
				t.Fatalf("multipart capture omitted its media policy: %q", got.stderr)
			}
			assertDebugCapturePrivate(t, got.stderr, "synthetic-private-upload", "@synthetic-upload.bin")
		})
	}
}

func TestMainDebugCaptureDelayedUploadReachesServerBeforeEOF(t *testing.T) {
	t.Setenv("GOMAXPROCS", "2")
	const prefix = "synthetic-private-upload-prefix"
	const tail = "-synthetic-private-upload-tail\x00\xff"
	var control mainDispatchResult
	for _, capture := range []bool{false, true} {
		t.Run(fmt.Sprintf("capture=%t", capture), func(t *testing.T) {
			prefixRead := make(chan []byte, 1)
			finished := make(chan struct{}, 1)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				defer func() {
					select {
					case finished <- struct{}{}:
					default:
						t.Error("unexpected extra upload completion")
					}
				}()
				reader, err := r.MultipartReader()
				if err != nil {
					t.Error(err)
					return
				}
				parts := map[string][]byte{}
				for {
					part, err := reader.NextPart()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Error(err)
						return
					}
					var first []byte
					if part.FormName() == "file" {
						first = make([]byte, len(prefix))
						if _, err := io.ReadFull(part, first); err != nil {
							t.Error(err)
							return
						}
						select {
						case prefixRead <- first:
						default:
							t.Error("unexpected extra upload file part")
						}
					}
					rest, err := io.ReadAll(part)
					part.Close()
					if err != nil {
						t.Error(err)
						return
					}
					parts[part.FormName()] = append(first, rest...)
				}
				if string(parts["file"]) != prefix+tail || string(parts["purpose"]) != "user_data" || len(parts) != 2 {
					t.Error("delayed upload bytes or multipart fields changed")
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"id":"file_synthetic","object":"file","bytes":%d,"filename":"anonymous_file","purpose":"user_data"}`, len(prefix)+len(tail))
			}))
			t.Cleanup(server.Close)
			t.Cleanup(server.CloseClientConnections)
			input, source, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { input.Close(); source.Close() })
			// Prefill before startup so stdin detection never races an empty pipe.
			// The small prefix fits the pipe; only server receipt permits the tail.
			if _, err := io.WriteString(source, prefix); err != nil {
				t.Fatal(err)
			}
			args := []string{"--format", "json", "files", "create", "--file", "-", "--purpose", "user_data"}
			if capture {
				args = append([]string{"--debug", "--debug-body"}, args...)
			}
			process, done, stdout, stderr := startShellCompletionCommand(t, server.URL, nil, input, args...)
			if err := input.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case first := <-prefixRead:
				if string(first) != prefix {
					t.Fatalf("first uploaded bytes=%q, want %q", first, prefix)
				}
			case err := <-done:
				t.Fatalf("upload exited before its source completed: %v; stderr=%q", err, readShellCompletionFile(t, stderr))
			case <-time.After(5 * time.Second):
				t.Fatal("upload did not deliver its prefix while source EOF was withheld")
			}
			if _, err := io.WriteString(source, tail); err != nil {
				t.Fatal(err)
			}
			if err := source.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("completed upload failed: %v; stderr=%q", err, readShellCompletionFile(t, stderr))
				}
			case <-time.After(5 * time.Second):
				t.Fatal("upload did not exit after its source closed")
			}
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				t.Fatal("upload retained its HTTP fixture after completion")
			}
			got := mainDispatchResult{process.ProcessState.ExitCode(), readShellCompletionFile(t, stdout), readShellCompletionFile(t, stderr)}
			if got.code != 0 || requests.Load() != 1 {
				t.Fatalf("delayed upload result=%+v requests=%d", got, requests.Load())
			}
			if !capture {
				control = got
				if got.stderr != "" {
					t.Fatalf("ordinary upload emitted diagnostics: %q", got.stderr)
				}
			} else {
				if got.stdout != control.stdout || !strings.Contains(got.stderr, "request body (redacted;") || !strings.Contains(got.stderr, "<omitted: non-JSON body>") {
					t.Fatalf("capture changed delayed upload output: control=%+v capture=%+v", control, got)
				}
				assertDebugCapturePrivate(t, got.stderr, prefix, "synthetic-private-upload-tail", "sk-fake-shell-completion")
			}
		})
	}
}

func TestMainDebugCaptureLargeRequestPreservesBytes(t *testing.T) {
	// This intentional 72 MiB request protects accepted payload sizes. The
	// diagnostic limit must never become an API request limit.
	payload := strings.Repeat("x", 72<<20)
	wantHash := sha256.Sum256([]byte(payload))
	file := filepath.Join(t.TempDir(), "large-input.txt")
	if err := os.WriteFile(file, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var request struct{ Model, Input string }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request.Model != "fake-model" || len(request.Input) != len(payload) || sha256.Sum256([]byte(request.Input)) != wantHash {
			t.Errorf("large request changed: model=%q input bytes=%d", request.Model, len(request.Input))
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_synthetic","object":"response","status":"completed","output":[]}`)
	}))
	t.Cleanup(server.Close)
	args := []string{"--format", "json", "responses", "create", "--model", "fake-model", "--input", "@" + file}
	control := runDebugCaptureCommand(t, server, args...)
	got := runDebugCaptureCommand(t, server, append([]string{"--debug", "--debug-body", "--debug-max-bytes", "1024"}, args...)...)
	if control.code != 0 || control.stderr != "" || got.code != control.code || got.stdout != control.stdout || requests.Load() != 2 {
		t.Fatalf("capture changed the large request: control=%+v capture=%+v requests=%d", control, got, requests.Load())
	}
	if !strings.Contains(got.stderr, "request body (redacted;") || !strings.Contains(got.stderr, "capture truncated") {
		t.Fatalf("large request capture lost its truncation notice: %q", got.stderr)
	}
	assertDebugCapturePrivate(t, got.stderr, "xxxxxxxxxxxxxxxx", file)
}

func TestMainDebugCaptureBinaryOutputAndSavePreserveBytes(t *testing.T) {
	payload := []byte("synthetic-private-binary\x00\xff\x1b[31m\r\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(payload)
	}))
	t.Cleanup(server.Close)
	for _, save := range []bool{false, true} {
		name := "stdout"
		if save {
			name = "save"
		}
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			destination := "-"
			if save {
				destination = filepath.Join(directory, "saved.bin")
			}
			args := []string{"files", "content", "file_synthetic", "--output", destination}
			control := runDebugCaptureCommand(t, server, args...)
			got := runDebugCaptureCommand(t, server, append([]string{"--debug", "--debug-body"}, args...)...)
			if control.code != 0 || got.code != control.code || got.stdout != control.stdout || !strings.HasSuffix(got.stderr, control.stderr) {
				t.Fatalf("capture changed binary output: control=%+v capture=%+v", control, got)
			}
			if save {
				data, err := os.ReadFile(destination)
				if err != nil || !bytes.Equal(data, payload) || got.stdout != "" {
					t.Fatalf("saved bytes changed: bytes=%v error=%v stdout=%q", data, err, got.stdout)
				}
				if !strings.Contains(control.stderr, "Wrote output to: ") || strings.Index(got.stderr, "response body (redacted;") > strings.Index(got.stderr, "Wrote output to: ") {
					t.Fatalf("binary diagnostics lost receipt ordering: %q", got.stderr)
				}
			} else if !bytes.Equal([]byte(got.stdout), payload) {
				t.Fatal("binary stdout bytes changed")
			}
			if !strings.Contains(got.stderr, "response body (redacted;") || !strings.Contains(got.stderr, "<omitted: non-JSON body>") {
				t.Fatalf("binary body capture lacks its omission label: %q", got.stderr)
			}
			assertDebugTimingStage(t, got.stderr, 1, "response body fully consumed", "")
			assertShellCompletionStagesAbsent(t, directory)
			assertDebugCapturePrivate(t, got.stderr, "synthetic-private-binary")
		})
	}
}

func TestMainDebugCaptureLargeSpeechEventPreservesWireBytes(t *testing.T) {
	// Speech already accepts SSE lines above the SDK decoder's separate
	// 32 MiB limit. Keep this single 72 MiB event and run it sequentially.
	payload := strings.Repeat("A", 72<<20)
	const prefix = "data: {\"type\":\"speech.audio.delta\",\"audio\":\""
	const suffix = "\"}\n\ndata: {\"type\":\"speech.audio.done\"}\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/audio/speech" {
			t.Errorf("unexpected speech request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, prefix)
		io.WriteString(w, payload)
		io.WriteString(w, suffix)
	}))
	t.Cleanup(server.Close)
	child, stdout, stderr, _ := startDebugTimingProcess(t, server, "--debug", "--debug-body", "--debug-sse", "--debug-event-timing",
		"--debug-max-bytes", "1024", "audio:speech", "create", "--model", "fake-audio-model", "--voice", "alloy",
		"--input", "synthetic-private-speech", "--stream-format", "sse", "--output", "-")
	gotHash := sha256.New()
	n, err := io.Copy(gotHash, stdout)
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("large speech event failed: %v; stderr=%q", err, stderr.String())
	}
	wantHash := sha256.New()
	io.WriteString(wantHash, prefix)
	io.WriteString(wantHash, payload)
	io.WriteString(wantHash, suffix)
	if n != int64(len(prefix)+len(payload)+len(suffix)) || !bytes.Equal(gotHash.Sum(nil), wantHash.Sum(nil)) {
		t.Fatalf("large speech wire bytes changed: got %d bytes", n)
	}
	log := stderr.String()
	if strings.Count(log, "complete frame observed after") != 2 || !strings.Contains(log, "SSE diagnostics truncated by capture limits") {
		t.Fatalf("large speech diagnostics lost timing or truncation: %q", log)
	}
	assertDebugTimingStage(t, log, 1, "response body fully consumed", "")
	assertDebugCapturePrivate(t, log, "AAAAAAAAAAAAAAAA", "synthetic-private-speech")
}

func TestMainDebugCaptureLargeSpeechExtractedOutput(t *testing.T) {
	// The raw-wire case also validates speech events. This separate 72 MiB
	// case exercises ShowJSONIterator's parsed and extracted output path.
	payload := strings.Repeat("A", 72<<20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"speech.audio.delta\",\"audio\":\"")
		io.WriteString(w, payload)
		io.WriteString(w, "\"}\n\ndata: {\"type\":\"speech.audio.done\"}\n\n")
	}))
	t.Cleanup(server.Close)
	args := []string{"--format", "text", "--transform", "type", "--raw-output", "audio:speech", "create",
		"--model", "fake-audio-model", "--voice", "alloy", "--input", "synthetic-private-speech", "--stream-format", "sse"}
	control := runDebugCaptureCommand(t, server, args...)
	flags := []string{"--debug", "--debug-body", "--debug-sse", "--debug-event-timing", "--debug-max-bytes", "1024"}
	got := runDebugCaptureCommand(t, server, append(flags, args...)...)
	if control.code != 0 || control.stderr != "" || control.stdout != "speech.audio.delta\nspeech.audio.done\n" || got.code != control.code || got.stdout != control.stdout {
		t.Fatalf("capture changed large parsed speech output: control=%+v capture=%+v", control, got)
	}
	if strings.Count(got.stderr, "complete frame observed after") != 2 || !strings.Contains(got.stderr, "SSE diagnostics truncated by capture limits") {
		t.Fatalf("large parsed speech lost timing or truncation: %q", got.stderr)
	}
	assertDebugTimingStage(t, got.stderr, 1, "response body fully consumed", "")
	assertDebugCapturePrivate(t, got.stderr, "AAAAAAAAAAAAAAAA", "synthetic-private-speech")
}
