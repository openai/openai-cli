package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestGeneratedBinaryEndpointsStreamChunkedResponses(t *testing.T) {
	tests := []struct {
		name    string
		group   string
		command cli.Command
		args    []string
		path    string
	}{
		{
			name:    "files",
			group:   "files",
			command: runtimeTestCommand("files", "content"),
			args:    []string{"--file-id", "file_123"},
			path:    "/files/file_123/content",
		},
		{
			name:    "videos",
			group:   "videos",
			command: runtimeTestCommand("videos", "download-content"),
			args:    []string{"--video-id", "video_123"},
			path:    "/videos/video_123/content",
		},
		{
			name:    "audio speech",
			group:   "audio:speech",
			command: runtimeTestCommand("audio:speech", "create"),
			args:    []string{"--input", "synthetic text", "--model", "tts-1", "--voice", "alloy"},
			path:    "/audio/speech",
		},
		{
			name:    "container files",
			group:   "containers:files:content",
			command: runtimeTestCommand("containers:files:content", "retrieve"),
			args:    []string{"--container-id", "container_123", "--file-id", "file_123"},
			path:    "/containers/container_123/files/file_123/content",
		},
		{
			name:    "skills",
			group:   "skills:content",
			command: runtimeTestCommand("skills:content", "retrieve"),
			args:    []string{"--skill-id", "skill_123"},
			path:    "/skills/skill_123/content",
		},
		{
			name:    "skill versions",
			group:   "skills:versions:content",
			command: runtimeTestCommand("skills:versions:content", "retrieve"),
			args:    []string{"--skill-id", "skill_123", "--version", "version_1"},
			path:    "/skills/skill_123/versions/version_1/content",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			const chunks = 32
			chunk := bytes.Repeat([]byte("synthetic-download-"), 512)
			requests := make(chan *http.Request, 1)
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				requests <- request
				response.Header().Set("Content-Type", "application/octet-stream")
				for index := 0; index < chunks; index++ {
					if _, err := response.Write(chunk); err != nil {
						return
					}
					response.(http.Flusher).Flush()
				}
			}))
			t.Cleanup(server.Close)

			outfile := filepath.Join(t.TempDir(), "download.bin")
			download := test.command
			command := &cli.Command{
				Name: "openai",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "debug"},
					&cli.StringFlag{Name: "base-url"},
					&cli.StringFlag{Name: "api-key"},
				},
				Commands: []*cli.Command{{Name: test.group, Commands: []*cli.Command{&download}}},
			}
			args := []string{
				"openai", "--base-url", server.URL + "/", "--api-key", "synthetic-test-key",
				test.group, download.Name,
			}
			args = append(args, test.args...)
			args = append(args, "--output", outfile)
			if err := command.Run(t.Context(), args); err != nil {
				t.Fatalf("generated %s download returned error: %v", test.name, err)
			}

			request := <-requests
			if request.URL.Path != test.path {
				t.Errorf("generated %s download path = %q, want %q", test.name, request.URL.Path, test.path)
			}
			if got := request.Header.Get("Authorization"); got != "Bearer synthetic-test-key" {
				t.Errorf("generated %s download Authorization = %q, want synthetic bearer token", test.name, got)
			}
			info, err := os.Stat(outfile)
			if err != nil {
				t.Fatalf("os.Stat(%q) returned error: %v", outfile, err)
			}
			if want := int64(chunks * len(chunk)); info.Size() != want {
				t.Errorf("generated %s download size = %d, want %d", test.name, info.Size(), want)
			}
		})
	}
}
