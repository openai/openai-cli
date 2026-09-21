package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestAudioResponsesPreserveCompletePlaintext(t *testing.T) {
	for _, endpoint := range []string{"transcriptions", "translations"} {
		for _, test := range []struct {
			name   string
			format string
			body   string
		}{
			{name: "text", format: "text", body: "first line\n世界 👋\n"},
			{name: "srt", format: "srt", body: "1\n00:00:00,000 --> 00:00:01,000\nhello\n\n"},
			{name: "vtt", format: "vtt", body: "WEBVTT\n\n00:00.000 --> 00:01.000\nhello\n"},
			{name: "empty", format: "text", body: ""},
			{name: "json_object", format: "text", body: `{"text":"still a transcript"}`},
			{name: "json_number", format: "text", body: "123\n"},
		} {
			t.Run(endpoint+"/"+test.name, func(t *testing.T) {
				output := runAudioResponseCommand(t, endpoint, test.format, "text/plain; charset=utf-8", test.body, "--format", "json")
				var got string
				if err := json.Unmarshal([]byte(output), &got); err != nil {
					t.Fatalf("%s create(%q) output = %q, want JSON string: %v", endpoint, test.format, output, err)
				}
				if got != test.body {
					t.Errorf("%s create(%q) text = %q, want %q", endpoint, test.format, got, test.body)
				}
			})
		}
	}
}

func TestAudioResponsesPreserveRawPlaintext(t *testing.T) {
	const body = "first line\n\x1b[31m世界\r\n"
	for _, endpoint := range []string{"transcriptions", "translations"} {
		t.Run(endpoint, func(t *testing.T) {
			got := runAudioResponseCommand(t, endpoint, "text", "text/plain", body, "--raw-output")
			if want := body + "\n"; got != want {
				t.Errorf("%s create(--raw-output) = %q, want %q", endpoint, got, want)
			}
		})
	}
}

func TestAudioResponsesPreserveOtherMedia(t *testing.T) {
	const body = `{ "text": "whole response", "segments": [{"speaker": "speaker_0"}], "extra": null }`
	for _, endpoint := range []string{"transcriptions", "translations"} {
		for _, media := range []string{"application/json", "", "application/octet-stream", "text/plain; charset", "text/plain; charset=utf-8; charset=ascii"} {
			t.Run(endpoint+"/"+media, func(t *testing.T) {
				got := runAudioResponseCommand(t, endpoint, "json", media, body, "--format", "raw")
				if want := body + "\n"; got != want {
					t.Errorf("%s create(Content-Type %q) = %q, want %q", endpoint, media, got, want)
				}
			})
		}
	}
}

func runAudioResponseCommand(t *testing.T, endpoint, format, media, body string, flags ...string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if want := "/audio/" + endpoint; r.URL.Path != want {
			t.Errorf("audio request path = %q, want %q", r.URL.Path, want)
		}
		w.Header()["Content-Type"] = []string{media}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	input := filepath.Join(dir, "audio.wav")
	if err := os.WriteFile(input, []byte("synthetic audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := os.Create(filepath.Join(dir, "output"))
	if err != nil {
		t.Fatal(err)
	}
	previousStdout := os.Stdout
	os.Stdout = output
	t.Cleanup(func() {
		os.Stdout = previousStdout
		output.Close()
	})
	t.Setenv("FORCE_COLOR", "0")
	audio := audioTranscriptionsCreate
	if endpoint == "translations" {
		audio = audioTranslationsCreate
	}
	command := &cli.Command{
		Name: "openai",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "debug"},
			&cli.StringFlag{Name: "base-url"},
			&cli.StringFlag{Name: "api-key"},
			&cli.StringFlag{Name: "format", Value: "auto"},
			&cli.BoolFlag{Name: "raw-output"},
		},
		Commands: []*cli.Command{{Name: "audio:" + endpoint, Commands: []*cli.Command{&audio}}},
	}
	args := append([]string{"openai", "--base-url", server.URL + "/", "--api-key", "synthetic-test-key"}, flags...)
	args = append(args, "audio:"+endpoint, "create", "--file", input, "--model", "whisper-1", "--response-format", format)
	if err := command.Run(t.Context(), args); err != nil {
		t.Fatalf("%s create(%q) error = %v, want success", endpoint, format, err)
	}
	got, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}
