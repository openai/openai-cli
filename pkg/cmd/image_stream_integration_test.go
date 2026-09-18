package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"image/color"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestImagesGenerateFriendlyStreamIntegration(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "openai")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "../../cmd/openai")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	_, partial := imageStreamTestPNG(t, color.RGBA{R: 200, A: 255})
	finalBytes, final := imageStreamTestPNG(t, color.RGBA{G: 200, A: 255})
	partialEvent := imageStreamTestEvent(t, "image_generation.partial_image", partial, 0)
	finalEvent := imageStreamTestEvent(t, "image_generation.completed", final, 0)
	for _, test := range []struct {
		name, stdin, events string
		flags               []string
		wantPartials        float64
		wantSaved           bool
	}{
		{"partial flag enables streaming", "", partialEvent + finalEvent, []string{"--partial-images", "2"}, 2, true},
		{"merged stdin enables streaming", `{"partial_images":2}`, partialEvent + finalEvent, nil, 2, true},
		{"explicit stream saves final", "", finalEvent, []string{"--stream", "true"}, 0, true},
		{"final event can arrive first", "", finalEvent, []string{"--partial-images", "3"}, 3, true},
		{"later error keeps final", "", finalEvent + "data: {\"error\":\"synthetic-private-prompt\"}\n\n", []string{"--partial-images", "1"}, 1, true},
		{"incomplete stream fails", "", partialEvent, []string{"--partial-images", "1"}, 1, false},
		{"stream error is private", "", "data: {\"error\":\"synthetic-private-prompt\"}\n\n", []string{"--partial-images", "1"}, 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var count atomic.Int32
			requests := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				if r.Method != "POST" || r.URL.Path != "/images/generations" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode synthetic request: %v", err)
				}
				requests <- body
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, test.events)
			}))
			t.Cleanup(server.Close)
			destination := t.TempDir()
			home := t.TempDir()
			args := []string{"--base-url", server.URL, "images", "generate", "--prompt", "synthetic image", "--output-dir", destination, "--name", "robot.png", "--inline", "off"}
			args = append(args, test.flags...)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, args...)
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				key = strings.ToUpper(key)
				if strings.HasPrefix(key, "OPENAI_") || key == "HOME" || key == "USERPROFILE" || key == "HTTP_PROXY" || key == "HTTPS_PROXY" || key == "ALL_PROXY" || key == "NO_PROXY" {
					continue
				}
				command.Env = append(command.Env, entry)
			}
			command.Env = append(command.Env, "OPENAI_API_KEY=synthetic-image-stream-key", "HOME="+home, "USERPROFILE="+home, "NO_PROXY=127.0.0.1,localhost")
			command.Stdin = strings.NewReader(test.stdin)
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			err := command.Run()
			if ctx.Err() != nil || test.wantSaved && err != nil || !test.wantSaved && err == nil {
				t.Fatalf("run = %v (context %v), want saved %v; stdout=%q stderr=%q", err, ctx.Err(), test.wantSaved, stdout.String(), stderr.String())
			}
			if count.Load() != 1 {
				t.Fatalf("request count = %d, want one", count.Load())
			}
			body := <-requests
			if body["stream"] != true || body["partial_images"] != test.wantPartials || body["n"] != float64(1) {
				t.Errorf("streaming request mismatch: stream=%v partials=%v n=%v", body["stream"], body["partial_images"], body["n"])
			}
			if body["model"] != defaultSavedImageModel {
				t.Errorf("model = %v, want saving preset", body["model"])
			}
			entries, err := os.ReadDir(destination)
			if err != nil {
				t.Fatal(err)
			}
			if test.wantSaved {
				if len(entries) != 1 || entries[0].Name() != "robot.png" || !strings.Contains(stdout.String(), "Saved image:") || stderr.Len() != 0 {
					t.Fatalf("final output mismatch: files=%v stdout=%q stderr=%q", entries, stdout.String(), stderr.String())
				}
				data, err := os.ReadFile(filepath.Join(destination, "robot.png"))
				if err != nil || !bytes.Equal(data, finalBytes) {
					t.Fatalf("final image differs: %v", err)
				}
			} else if len(entries) != 0 || !strings.Contains(stderr.String(), "final image") || !strings.Contains(stderr.String(), "API usage") {
				t.Fatalf("incomplete stream mismatch: files=%v stdout=%q stderr=%q", entries, stdout.String(), stderr.String())
			}
			for _, unwanted := range []string{"synthetic-private-prompt", "b64_json", "image_generation.partial_image", "\x1b", "Generating image...", "Progress preview"} {
				if strings.Contains(stdout.String()+stderr.String(), unwanted) {
					t.Errorf("saved/non-terminal stream exposed %q", unwanted)
				}
			}
		})
	}
	t.Run("terminal automatically previews and saves", func(t *testing.T) {
		if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
			t.Skip("PTY integration uses Unix script")
		}
		script, err := exec.LookPath("script")
		if err != nil {
			t.Skip("script is unavailable for PTY integration")
		}
		var count atomic.Int32
		requests := make(chan map[string]any, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count.Add(1)
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode synthetic request: %v", err)
			}
			requests <- body
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, partialEvent)
			w.(http.Flusher).Flush()
			io.WriteString(w, imageStreamTestEvent(t, "image_generation.partial_image", partial, 1))
			w.(http.Flusher).Flush()
			io.WriteString(w, finalEvent)
		}))
		t.Cleanup(server.Close)
		args := []string{binary, "--base-url", server.URL, "images", "generate", "--prompt", "synthetic image", "--partial-images", "2"}
		if runtime.GOOS == "darwin" {
			args = append([]string{"-q", "/dev/null"}, args...)
		} else {
			quoted := make([]string, len(args))
			for index, arg := range args {
				quoted[index] = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
			}
			args = []string{"-q", "-e", "-c", strings.Join(quoted, " "), "/dev/null"}
		}
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, script, args...)
		command.WaitDelay = time.Second
		command.Stdin = strings.NewReader("")
		home := t.TempDir()
		for _, entry := range os.Environ() {
			key, _, _ := strings.Cut(entry, "=")
			key = strings.ToUpper(key)
			if strings.HasPrefix(key, "OPENAI_") || key == "HOME" || key == "USERPROFILE" || key == "HTTP_PROXY" || key == "HTTPS_PROXY" || key == "ALL_PROXY" || key == "NO_PROXY" ||
				key == "FORCE_COLOR" || key == "NO_COLOR" || key == "TERM_PROGRAM" || key == "TERM_PROGRAM_VERSION" || key == "TERM" || key == "COLORTERM" || key == "CI" || key == "TMUX" || key == "STY" || key == "ZELLIJ" {
				continue
			}
			command.Env = append(command.Env, entry)
		}
		command.Env = append(command.Env, "OPENAI_API_KEY=synthetic-image-stream-key", "HOME="+home, "USERPROFILE="+home, "NO_PROXY=127.0.0.1,localhost", "TERM_PROGRAM=ghostty", "TERM=xterm-256color", "FORCE_COLOR=0", "NO_COLOR=1")
		output, err := command.CombinedOutput()
		if err != nil || ctx.Err() != nil {
			t.Fatalf("terminal command failed: %v, context=%v, output=%q", err, ctx.Err(), output)
		}
		if count.Load() != 1 {
			t.Fatalf("request count = %d, want one", count.Load())
		}
		body := <-requests
		if body["model"] != defaultSavedImageModel || body["stream"] != true || body["partial_images"] != float64(2) || body["n"] != float64(1) {
			t.Errorf("terminal request mismatch: model=%v stream=%v partials=%v n=%v", body["model"], body["stream"], body["partial_images"], body["n"])
		}
		text := string(output)
		for _, want := range []string{"Progress preview 1 of 2:", "Progress preview 2 of 2:", "Saved image:"} {
			if !strings.Contains(text, want) {
				t.Errorf("terminal output missing %q", want)
			}
		}
		if strings.Count(text, "\x1b_Ga=T") != 3 {
			t.Errorf("terminal output should render two partial images and one final image")
		}
		if strings.Index(text, "Progress preview 2 of 2:") > strings.Index(text, "Saved image:") {
			t.Error("interim previews appeared after final save")
		}
		for _, unwanted := range []string{"b64_json", "image_generation.partial_image", "image_generation.completed", "Enable sharp images"} {
			if strings.Contains(text, unwanted) {
				t.Errorf("terminal output exposed raw events or native setup: %q", unwanted)
			}
		}
		destination := filepath.Join(home, "Downloads", "gpt-images")
		entries, err := os.ReadDir(destination)
		if err != nil || len(entries) != 1 || entries[0].Name() != "synthetic-image.png" {
			t.Fatalf("automatic image folder contains unexpected files: %v, %v", entries, err)
		}
		path := filepath.Join(destination, entries[0].Name())
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, finalBytes) || !strings.Contains(text, path) {
			t.Fatalf("automatic final save differs or its path is missing: %v", err)
		}
	})
}
