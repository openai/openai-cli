//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestMainReadableAudioInterruptClosesStream(t *testing.T) {
	for _, command := range []string{"audio:transcriptions", "audio:speech"} {
		t.Run(command, func(t *testing.T) {
			disconnected := make(chan struct{})
			event := `{"type":"transcript.text.delta","delta":"Visible before interrupt"}`
			extra := []string{"--stream=true"}
			prefix := "Visible before interrupt"
			if command == "audio:speech" {
				event = `{"type":"speech.audio.delta","audio":"c3ludGhldGlj"}`
				extra = []string{"--stream-format", "sse"}
				prefix = "Type: speech.audio.delta"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				writeStreamingTextEvent(w, event)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(disconnected)
			}))
			t.Cleanup(server.Close)
			child, stdout, _, ctx := startStreamingTextCommand(t, server, readableAudioArgs(t, command, extra...)...)
			readStreamingTextPrefix(t, ctx, stdout, prefix)
			if err := child.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			if err := child.Wait(); err == nil {
				t.Fatal("interrupted audio command reported success")
			}
			select {
			case <-disconnected:
			case <-ctx.Done():
				t.Fatal("interrupted audio command left the HTTP stream open")
			}
		})
	}
}

func TestMainReadableAudioClosedStdoutClosesStream(t *testing.T) {
	for _, command := range []string{"audio:transcriptions", "audio:speech"} {
		for _, format := range []string{"text", "jsonl", "raw"} {
			t.Run(command+"/"+format, func(t *testing.T) {
				disconnected := make(chan struct{})
				event := `{"type":"transcript.text.delta","delta":"` + strings.Repeat("x", 8000) + `"}`
				extra := []string{"--stream=true"}
				if command == "audio:speech" {
					event = `{"type":"speech.audio.delta","audio":"c3ludGhldGlj","future":"` + strings.Repeat("x", 8000) + `"}`
					extra = []string{"--stream-format", "sse"}
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					writeStreamingTextEvent(w, event)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					close(disconnected)
				}))
				t.Cleanup(server.Close)
				reader, writer, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				reader.Close()
				defer writer.Close()
				binary, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				args := append([]string{"-test.run=^TestMainDispatchProcess$", "--", "openai", "--format", format}, readableAudioArgs(t, command, extra...)...)
				child := exec.CommandContext(ctx, binary, args...)
				child.Env = []string{"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_API_KEY=sk-fake-audio-pipe-test", "OPENAI_BASE_URL=" + server.URL, "FORCE_COLOR=0"}
				var stderr bytes.Buffer
				child.Stdout, child.Stderr = writer, &stderr
				if err := child.Run(); err != nil || ctx.Err() != nil || stderr.Len() != 0 {
					t.Fatalf("closed audio output must exit quietly: %v, stderr=%q", err, stderr.String())
				}
				select {
				case <-disconnected:
				case <-ctx.Done():
					t.Fatal("closed audio output left the HTTP stream open")
				}
			})
		}
	}
}

func TestMainReadableAudioSpeechFailureSurvivesClosedStdout(t *testing.T) {
	for _, format := range []string{"text", "jsonl", "raw"} {
		t.Run(format, func(t *testing.T) {
			disconnected := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				writeStreamingTextEvent(w, `{"type":"error","error":{"message":"synthetic-private-detail https://secret.invalid/?token=fake\u001b[2J"}}`)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(disconnected)
			}))
			t.Cleanup(server.Close)
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			reader.Close()
			defer writer.Close()
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			args := append([]string{"-test.run=^TestMainDispatchProcess$", "--", "openai", "--format", format}, readableAudioArgs(t, "audio:speech", "--stream-format", "sse")...)
			child := exec.CommandContext(ctx, binary, args...)
			child.Env = []string{"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_API_KEY=sk-fake-audio-pipe-test", "OPENAI_BASE_URL=" + server.URL, "FORCE_COLOR=0"}
			var stderr bytes.Buffer
			child.Stdout, child.Stderr = writer, &stderr
			if err := child.Run(); ctx.Err() != nil || child.ProcessState == nil || child.ProcessState.ExitCode() != 1 {
				t.Fatalf("speech failure with closed stdout must exit 1: %v, stderr=%q", err, stderr.String())
			}
			message := strings.TrimSpace(stderr.String())
			if format != "text" {
				var diagnostic struct {
					Message string `json:"message"`
				}
				if err := json.Unmarshal(stderr.Bytes(), &diagnostic); err != nil {
					t.Fatalf("invalid structured audio diagnostic: %v", err)
				}
				message = diagnostic.Message
			}
			if message != "the API reported an error while streaming\nOutput may be incomplete." {
				t.Fatalf("speech failure diagnostic = %q", message)
			}
			for _, private := range []string{"synthetic-private-detail", "secret.invalid", "token=", "\x1b"} {
				if strings.Contains(stderr.String(), private) {
					t.Fatalf("audio diagnostic exposed API data: %q", stderr.String())
				}
			}
			select {
			case <-disconnected:
			case <-ctx.Done():
				t.Fatal("failed audio output left the HTTP stream open")
			}
		})
	}
}
