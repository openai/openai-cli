package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/openai/openai-cli/internal/modeldefaults"
	"github.com/tidwall/gjson"
)

const realtimeSpeechModel = modeldefaults.Speech
const syntheticScript = "@synthetic-untrusted-script 雪"

func serveRealtimeSpeech(t *testing.T, wantAuthorization string, sendAudio func(context.Context, *websocket.Conn)) *httptest.Server {
	t.Helper()
	var connections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connections.Add(1)
		if r.Method != "GET" || r.URL.Path != "/custom/v1/realtime" || r.URL.Query().Get("model") != realtimeSpeechModel {
			t.Errorf("unexpected speech route: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != wantAuthorization || r.Header.Get("OpenAI-Project") != "proj_synthetic" {
			t.Error("realtime speech did not preserve the requested key and project")
			http.Error(w, "", http.StatusUnauthorized)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		write := func(event string) bool {
			if err := conn.Write(ctx, websocket.MessageText, []byte(event)); err != nil {
				t.Error(err)
				return false
			}
			return true
		}
		if !write(`{"type":"session.created","session":{"type":"realtime"}}`) {
			return
		}
		read := func() gjson.Result {
			_, wire, err := conn.Read(ctx)
			if err != nil {
				t.Error(err)
				return gjson.Result{}
			}
			return gjson.ParseBytes(wire)
		}
		config := read()
		if config.Get("type").Str != "session.update" || config.Get("session.audio.output.voice").Str != "coral" ||
			config.Get("session.audio.output.format.type").Str != "audio/pcm" || config.Get("session.audio.output.format.rate").Int() != 24000 {
			t.Errorf("unexpected session settings: %s", config.Raw)
			return
		}
		if !write(`{"type":"session.updated","session":{"type":"realtime"}}`) {
			return
		}
		create := read()
		if create.Get("type").Str != "response.create" || create.Get("response.input.0.content.0.text").Str != syntheticScript ||
			create.Get("response.conversation").Str != "none" || create.Get("response.output_modalities.0").Str != "audio" {
			t.Errorf("unexpected response request: %s", create.Raw)
			return
		}
		if write(`{"type":"response.created","response":{"id":"resp_synthetic","status":"in_progress"}}`) {
			sendAudio(ctx, conn)
		}
	}))
	t.Cleanup(func() {
		server.Close()
		if n := connections.Load(); n != 1 {
			t.Errorf("sent %d speech requests, want exactly one", n)
		}
	})
	return server
}

func writeSpeechEvent(t *testing.T, ctx context.Context, conn *websocket.Conn, event string) {
	t.Helper()
	if err := conn.Write(ctx, websocket.MessageText, []byte(event)); err != nil {
		t.Error(err)
	}
}

func speechTestCommand(server *httptest.Server) []string {
	return []string{"openai", "--base-url", server.URL + "/custom/v1/", "--project", "proj_synthetic", "speak"}
}

func speechTestArgs(server *httptest.Server, output, format string) []string {
	return append(speechTestCommand(server), "--model", realtimeSpeechModel, "--input", "\\"+syntheticScript, "--voice", "coral", "--response-format", format, "--output", output)
}

func TestMainSpeakRealtime(t *testing.T) {
	pcm := []byte{0x42, 0x80, 0x53, 0x00}
	for _, tc := range []struct {
		name, format, status, header string
	}{
		{"PCM stdout", "pcm", "completed", ""},
		{"debug trace", "pcm", "completed", ""},
		{"explicit Authorization override", "pcm", "completed", "Bearer synthetic-override"},
		{"explicit Authorization removal", "pcm", "completed", "remove"},
		{"WAV file", "wav", "completed", ""},
		{"incomplete after audio", "wav", "incomplete", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantAuthorization := "Bearer synthetic-speech-key"
			if tc.header != "" {
				wantAuthorization = tc.header
			}
			if tc.header == "remove" {
				wantAuthorization = ""
			}
			server := serveRealtimeSpeech(t, wantAuthorization, func(ctx context.Context, conn *websocket.Conn) {
				writeSpeechEvent(t, ctx, conn, `{"type":"response.output_audio_transcript.delta","response_id":"resp_synthetic","delta":"Read this line."}`)
				for _, chunk := range [][]byte{pcm[:2], pcm[2:]} {
					writeSpeechEvent(t, ctx, conn, `{"type":"response.output_audio.delta","response_id":"resp_synthetic","delta":"`+base64.StdEncoding.EncodeToString(chunk)+`"}`)
				}
				writeSpeechEvent(t, ctx, conn, `{"type":"response.output_audio.done","response_id":"resp_synthetic"}`)
				writeSpeechEvent(t, ctx, conn, `{"type":"response.done","response":{"id":"resp_synthetic","status":"`+tc.status+`"}}`)
			})
			out := "-"
			if tc.format == "wav" {
				out = filepath.Join(t.TempDir(), "speech.wav")
			}
			env := []string{"OPENAI_API_KEY=synthetic-speech-key", "OPENAI_ADMIN_KEY=synthetic-unused-admin"}
			args := speechTestArgs(server, out, tc.format)
			if tc.header != "" {
				args = append([]string{"openai", "--header", "Authorization: " + wantAuthorization}, args[1:]...)
			}
			debug := tc.name == "debug trace"
			if debug {
				args = append([]string{"openai", "--debug", "--debug-body"}, args[1:]...)
			}
			var input *os.File
			if tc.name == "WAV file" {
				// This is the protected piped request printed by the composer.
				args = append(speechTestCommand(server), "--output", out)
				env = append(env, "OPENAI_UNTRUSTED_STDIN=true")
				file, err := os.CreateTemp(t.TempDir(), "request")
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				if err := json.NewEncoder(file).Encode(map[string]any{
					"input": syntheticScript, "model": realtimeSpeechModel, "voice": "coral", "response_format": "wav", "speed": 1,
				}); err != nil {
					t.Fatal(err)
				}
				if _, err := file.Seek(0, 0); err != nil {
					t.Fatal(err)
				}
				input = file
			}
			got := runMainDispatchWithStdin(t, "bash", env, input, args...)
			if tc.status != "completed" {
				if got.code == 0 || got.stderr == "" || strings.Contains(got.stderr, syntheticScript) || got.stdout != "" {
					t.Fatalf("unexpected failure output: %+v", got)
				}
				if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("incomplete speech left a destination: %v", err)
				}
				return
			}
			if got.code != 0 {
				t.Fatalf("speech failed: %s", got.stderr)
			}
			if out == "-" {
				if debug {
					if strings.Count(got.stderr, "GET /custom/v1/realtime HTTP/") != 1 || strings.Contains(got.stderr, "POST /custom/v1/audio/speech") {
						t.Error("debug trace did not match the single observed WebSocket handshake")
					}
					for _, private := range []string{syntheticScript, "synthetic-speech-key", "synthetic-unused-admin"} {
						if strings.Contains(got.stderr, private) {
							t.Fatal("private speech request appeared in debug trace")
						}
					}
				}
				if !bytes.Equal([]byte(got.stdout), pcm) || (!debug && got.stderr != "") {
					t.Fatalf("PCM stdout=%x stderr=%q", got.stdout, got.stderr)
				}
				return
			}
			wave, err := os.ReadFile(out)
			if err != nil || len(wave) < 44 || string(wave[:4]) != "RIFF" || string(wave[8:12]) != "WAVE" || binary.LittleEndian.Uint32(wave[24:28]) != 24000 || !bytes.HasSuffix(wave, pcm) {
				t.Fatalf("unexpected WAV: %x, err=%v", wave, err)
			}
		})
	}
}

func TestMainSpeakRealtimeInterrupt(t *testing.T) {
	connected, closed := make(chan struct{}), make(chan struct{})
	server := serveRealtimeSpeech(t, "Bearer synthetic-speech-key", func(ctx context.Context, conn *websocket.Conn) {
		close(connected)
		_, _, _ = conn.Read(ctx)
		close(closed)
	})
	dir := t.TempDir()
	out := filepath.Join(dir, "speech.wav")
	binaryPath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	child, interrupt, verify := imageEditInterruptCommand(t, ctx, binaryPath, dir,
		[]string{"OPENAI_API_KEY=synthetic-speech-key", "HOME=" + dir, "USERPROFILE=" + dir}, speechTestArgs(server, out, "wav")...)
	var diagnostics bytes.Buffer
	child.Stderr = &diagnostics
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	select {
	case <-connected:
	case err := <-done:
		t.Fatalf("speech exited before interrupt: %v %s", err, &diagnostics)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	started := time.Now()
	if err := interrupt(); err != nil {
		t.Fatal(err)
	}
	waitErr := <-done
	if err := verify(waitErr, diagnostics.Bytes(), time.Since(started)); err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	if !errors.As(waitErr, &exit) || exit.ExitCode() != 130 {
		t.Fatalf("interrupt exit=%v stderr=%s", waitErr, &diagnostics)
	}
	select {
	case <-closed:
	case <-ctx.Done():
		t.Error("socket remained open after interrupt")
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("interrupt left a destination: %v", err)
	}
}
