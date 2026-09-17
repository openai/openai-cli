package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Handwritten integration coverage: run the public executable against a local
// server so flag parsing, piped input, SDK serialization, and file output agree.
func TestImagesGenerateOutputIntegration(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "openai")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "../../cmd/openai")
	buildOutput, err := build.CombinedOutput()
	require.NoError(t, err, "building CLI: %s", buildOutput)

	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var pngBuffer bytes.Buffer
	require.NoError(t, png.Encode(&pngBuffer, img))
	pngBytes := pngBuffer.Bytes()
	encodedImage := base64.StdEncoding.EncodeToString(pngBytes)
	response, err := json.Marshal(map[string]any{
		"created": 123,
		"data":    []map[string]string{{"b64_json": encodedImage}},
	})
	require.NoError(t, err)

	type request struct {
		method, path string
		body         []byte
	}
	newServer := func(t *testing.T) (*httptest.Server, <-chan request, *atomic.Int32) {
		t.Helper()
		requests := make(chan request, 4)
		count := &atomic.Int32{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count.Add(1)
			body, readErr := io.ReadAll(r.Body)
			if readErr != nil {
				http.Error(w, "could not read synthetic request", http.StatusBadRequest)
				return
			}
			requests <- request{method: r.Method, path: r.URL.Path, body: body}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(response)
		}))
		t.Cleanup(server.Close)
		return server, requests, count
	}
	runImageCommand := func(t *testing.T, serverURL, stdin string, rootFlags, flags []string, terminal, operation string) (string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		args := append([]string{"--base-url", serverURL}, rootFlags...)
		args = append(args, "images", operation)
		args = append(args, flags...)
		executable := binary
		if terminal != "" {
			if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
				t.Skip("PTY integration uses Unix script")
			}
			var err error
			executable, err = exec.LookPath("script")
			if err != nil {
				t.Skip("script is unavailable for PTY integration")
			}
			if runtime.GOOS == "darwin" {
				args = append([]string{"-q", "/dev/null", binary}, args...)
			} else {
				// Linux script accepts one shell command; quote every argument,
				// including temporary paths, rather than interpolating shell code.
				quoted := make([]string, 0, len(args)+1)
				for _, arg := range append([]string{binary}, args...) {
					quoted = append(quoted, "'"+strings.ReplaceAll(arg, "'", "'\\''")+"'")
				}
				args = []string{"-q", "-e", "-c", strings.Join(quoted, " "), "/dev/null"}
			}
		}
		command := exec.CommandContext(ctx, executable, args...)
		command.Stdin = strings.NewReader(stdin)
		// A timed-out PTY wrapper can leave descendants holding its pipes.
		// Bound output draining so failures remain visible to the test runner.
		command.WaitDelay = time.Second
		command.Dir = t.TempDir()
		for _, value := range os.Environ() {
			key, _, _ := strings.Cut(value, "=")
			key = strings.ToUpper(key)
			if strings.HasPrefix(key, "OPENAI_") || key == "FORCE_COLOR" || key == "NO_COLOR" || key == "TERM_PROGRAM" || key == "TERM_PROGRAM_VERSION" || key == "COLORTERM" ||
				key == "TERM" || key == "CI" || key == "TMUX" || key == "STY" || key == "ZELLIJ" || key == "HOME" || key == "USERPROFILE" ||
				key == "HTTP_PROXY" || key == "HTTPS_PROXY" || key == "ALL_PROXY" || key == "NO_PROXY" {
				continue
			}
			command.Env = append(command.Env, value)
		}
		program := terminal
		program, version, _ := strings.Cut(program, ":")
		if program == "" {
			program = "ghostty"
		}
		noColor := "1"
		if program == "Apple_Terminal" {
			noColor = ""
		}
		if operation == "generate" {
			command.Env = append(command.Env, "OPENAI_API_KEY=synthetic-image-output-key")
		}
		command.Env = append(command.Env, "FORCE_COLOR=0", "NO_COLOR="+noColor, "CLICOLOR=1", "TERM_PROGRAM="+program, "TERM_PROGRAM_VERSION="+version, "TERM=xterm-256color", "NO_PROXY=127.0.0.1,localhost", "HOME="+command.Dir, "USERPROFILE="+command.Dir)
		var output []byte
		var runErr error
		if strings.HasPrefix(terminal, "Apple_Terminal") && stdin == "" {
			// Reply only after the setup question. Earlier terminal color queries
			// can consume eagerly supplied input. Never accept native setup here.
			input, answer, err := os.Pipe()
			require.NoError(t, err)
			defer input.Close()
			defer answer.Close()
			capture := &imageTestDeclineWriter{answer: answer}
			command.Stdin = input
			command.Stdout, command.Stderr = capture, capture
			runErr = command.Run()
			output = capture.Bytes()
		} else {
			output, runErr = command.CombinedOutput()
		}
		if terminal == "" {
			require.NotContains(t, string(output), "\x1b", "redirected output must never contain terminal graphics")
		}
		return string(output), runErr
	}
	runWithTerminal := func(t *testing.T, serverURL, stdin string, rootFlags, flags []string, terminal string) (string, error) {
		return runImageCommand(t, serverURL, stdin, rootFlags, flags, terminal, "generate")
	}
	run := func(t *testing.T, serverURL, stdin string, rootFlags, flags []string) (string, error) {
		return runWithTerminal(t, serverURL, stdin, rootFlags, flags, "")
	}
	readRequest := func(t *testing.T, requests <-chan request) map[string]any {
		t.Helper()
		select {
		case got := <-requests:
			require.Equal(t, "POST", got.method)
			require.Equal(t, "/images/generations", got.path)
			var body map[string]any
			require.NoError(t, json.Unmarshal(got.body, &body))
			require.NotContains(t, body, "output_dir")
			require.NotContains(t, body, "output-dir")
			require.NotContains(t, body, "no-preview")
			require.NotContains(t, body, "no_preview")
			require.NotContains(t, body, "inline")
			require.NotContains(t, body, "name")
			require.NotContains(t, body, "open")
			return body
		default:
			t.Fatal("CLI did not send a request")
			return nil
		}
	}
	t.Run("name alone enables saving", func(t *testing.T) {
		server, requests, count := newServer(t)
		output, err := run(t, server.URL, "", nil, []string{"--prompt", "A red pixel", "--name", "orange-robot"})
		require.NoError(t, err)
		require.EqualValues(t, 1, count.Load())
		require.Contains(t, output, "Saved image:")
		require.Contains(t, output, "orange-robot.png")
		require.NotContains(t, output, encodedImage)
		require.Equal(t, defaultSavedImageModel, readRequest(t, requests)["model"])
	})
	for _, test := range []struct {
		name, terminal, marker string
		rootFlags, flags       []string
	}{
		{"TTY Ghostty auto preview", "ghostty", "\x1b_Ga=T", nil, nil},
		{"TTY iTerm2 auto preview", "iTerm.app", "\x1b]1337;File=", nil, nil},
		{"TTY inline on", "ghostty", "\x1b_Ga=T", nil, []string{"--inline", "on"}},
		{"TTY inline off", "ghostty", "", nil, []string{"--inline", "off"}},
		{"TTY Apple Terminal text", "Apple_Terminal", "", nil, nil},
		{"TTY Apple Terminal RGB", "Apple_Terminal:470.2", "", nil, nil},
		{"TTY Apple Terminal off", "Apple_Terminal", "", nil, []string{"--inline", "off"}},
		{"TTY explicit JSON", "ghostty", "", []string{"--format", "json"}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, requests, count := newServer(t)
			output, runErr := runWithTerminal(t, server.URL, "", test.rootFlags, append([]string{"--prompt", "A red pixel"}, test.flags...), test.terminal)
			require.NoError(t, runErr, "PTY command failed")
			require.EqualValues(t, 1, count.Load())
			body := readRequest(t, requests)
			if test.rootFlags != nil {
				require.NotContains(t, body, "model")
				require.Contains(t, output, encodedImage)
				require.NotContains(t, output, "Saved image:")
			} else {
				require.Equal(t, defaultSavedImageModel, body["model"])
				require.Contains(t, output, "Saved image:")
			}
			if test.marker == "" {
				// The CLI's existing color detection may query the terminal;
				// only graphics sequences belong to this feature.
				require.NotContains(t, output, "\x1b_G")
				require.NotContains(t, output, "\x1b]1337;")
			} else {
				require.Contains(t, output, test.marker)
			}
			if test.name == "TTY Apple Terminal RGB" {
				require.Contains(t, output, "Inline preview (text approximation):")
				require.Contains(t, output, "\x1b[38;2;")
				require.NotContains(t, output, "\x1b[38;5;")
			} else if test.name == "TTY Apple Terminal text" {
				require.Contains(t, output, "Inline preview (text approximation):")
				require.Contains(t, output, "\x1b[38;5;")
			} else {
				require.NotContains(t, output, "Inline preview (text approximation):")
			}
		})
	}
	t.Run("preview saved image without key or API request", func(t *testing.T) {
		server, _, count := newServer(t)
		path := filepath.Join(t.TempDir(), "saved image.png")
		require.NoError(t, os.WriteFile(path, pngBytes, 0600))
		output, runErr := runImageCommand(t, server.URL, "", []string{"--format", "AUTO"}, []string{path}, "Apple_Terminal:470.2", "preview")
		require.NoError(t, runErr, output)
		require.Zero(t, count.Load())
		require.Contains(t, output, "Image:")
		require.Contains(t, output, "Inline preview (text approximation):")
		require.Contains(t, output, "\x1b[38;2;")
		require.NotContains(t, output, "Saved image:")
		saved, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		require.Equal(t, pngBytes, saved)
	})
	for _, test := range []struct {
		name, message string
		args          []string
	}{
		{"preview missing file argument", "provide one image file", nil},
		{"preview extra argument", "provide one image file", []string{"one.png", "two.png"}},
		{"preview redirected output", "require terminal output", []string{"one.png"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, _, count := newServer(t)
			output, runErr := runImageCommand(t, server.URL, "", nil, test.args, "", "preview")
			require.Error(t, runErr)
			require.Contains(t, output, test.message)
			require.Zero(t, count.Load())
		})
	}

	for _, test := range []struct {
		name, stdin, model, responseFormat string
		flags                              []string
	}{
		{name: "default save model", flags: []string{"--prompt", "A red pixel"}, model: "gpt-image-2.5-sunburst"},
		{name: "no preview is CLI only", flags: []string{"--prompt", "A red pixel", "--no-preview"}, model: "gpt-image-2.5-sunburst"},
		{name: "inline off is CLI only", flags: []string{"--prompt", "A red pixel", "--inline", "off"}, model: "gpt-image-2.5-sunburst"},
		{name: "named image", flags: []string{"--prompt", "A red pixel", "--name", "orange-robot"}, model: "gpt-image-2.5-sunburst"},
		{name: "explicit model", flags: []string{"--prompt", "A red pixel", "--model", "gpt-image-1.5"}, model: "gpt-image-1.5"},
		{name: "piped model", stdin: `{"prompt":"A red pixel","model":"gpt-image-1"}`, model: "gpt-image-1"},
		{name: "flag overrides piped model", stdin: `{"prompt":"A red pixel","model":"gpt-image-1"}`, flags: []string{"--model", "gpt-image-1.5"}, model: "gpt-image-1.5"},
		{name: "DALL-E requests base64", flags: []string{"--prompt", "A red pixel", "--model", "dall-e-3"}, model: "dall-e-3", responseFormat: "b64_json"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, requests, count := newServer(t)
			outputDir := t.TempDir()
			flags := append([]string{"--output-dir", outputDir}, test.flags...)
			output, runErr := run(t, server.URL, test.stdin, nil, flags)
			require.NoError(t, runErr, output)
			require.EqualValues(t, 1, count.Load())
			body := readRequest(t, requests)
			require.Equal(t, test.model, body["model"])
			require.Equal(t, "A red pixel", body["prompt"])
			if test.responseFormat != "" {
				require.Equal(t, test.responseFormat, body["response_format"])
			}
			files, readErr := os.ReadDir(outputDir)
			require.NoError(t, readErr)
			require.Len(t, files, 1)
			savedPath := filepath.Join(outputDir, files[0].Name())
			saved, readErr := os.ReadFile(savedPath)
			require.NoError(t, readErr)
			require.Equal(t, pngBytes, saved)
			require.Equal(t, ".png", filepath.Ext(savedPath))
			if test.name == "named image" {
				require.Equal(t, "orange-robot.png", files[0].Name())
			} else {
				require.Regexp(t, `^image-\d{4}-\d{2}-\d{2}-\d{6}\.png$`, files[0].Name())
			}
			require.True(t, strings.Contains(output, savedPath) || strings.Contains(output, strconv.Quote(savedPath)), "output must identify the saved path: %s", output)
			require.NotContains(t, output, encodedImage)
		})
	}

	for _, test := range []struct {
		name      string
		rootFlags []string
	}{
		{name: "nonterminal keeps JSON"},
		{name: "explicit JSON", rootFlags: []string{"--format", "json"}},
		{name: "explicit YAML", rootFlags: []string{"--format", "yaml"}},
		{name: "raw transform", rootFlags: []string{"--transform", "data.0.b64_json", "--raw-output"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, requests, _ := newServer(t)
			output, runErr := run(t, server.URL, "", test.rootFlags, []string{"--prompt", "A red pixel"})
			require.NoError(t, runErr, output)
			body := readRequest(t, requests)
			require.NotContains(t, body, "model", "ordinary API output must retain server model selection")
			require.Contains(t, output, encodedImage)
			switch test.name {
			case "explicit YAML":
				require.Contains(t, output, "b64_json:")
			case "raw transform":
				require.Equal(t, encodedImage, strings.TrimSpace(output))
			default:
				require.JSONEq(t, string(response), output)
			}
		})
	}

	for _, test := range []struct {
		name             string
		rootFlags, flags []string
		stdin            string
		missingDir       bool
	}{
		{name: "missing output directory", missingDir: true},
		{name: "invalid inline", flags: []string{"--inline", "maybe"}},
		{name: "open conflicts with JSON", rootFlags: []string{"--format", "json"}, flags: []string{"--open"}},
		{name: "open conflicts with streaming", flags: []string{"--open", "--stream", "true"}},
		{name: "empty inline", flags: []string{"--inline", ""}},
		{name: "conflicting preview flags", flags: []string{"--inline", "on", "--no-preview"}},
		{name: "name path traversal", flags: []string{"--name", "../robot"}},
		{name: "empty name", flags: []string{"--name", ""}},
		{name: "JSON conflicts", rootFlags: []string{"--format", "json"}},
		{name: "YAML conflicts", rootFlags: []string{"--format", "yaml"}},
		{name: "transform conflicts", rootFlags: []string{"--transform", "data"}},
		{name: "raw output conflicts", rootFlags: []string{"--raw-output"}},
		{name: "stream conflicts", flags: []string{"--stream", "true"}},
		{name: "piped stream conflicts", stdin: `{"stream":true}`},
		{name: "URL response conflicts", flags: []string{"--model", "dall-e-3", "--response-format", "url"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, _, count := newServer(t)
			outputDir := t.TempDir()
			if test.missingDir {
				outputDir = filepath.Join(outputDir, "missing")
			}
			flags := append([]string{"--prompt", "A red pixel", "--output-dir", outputDir}, test.flags...)
			output, runErr := run(t, server.URL, test.stdin, test.rootFlags, flags)
			require.Error(t, runErr, output)
			require.NotEmpty(t, strings.TrimSpace(output))
			require.Zero(t, count.Load(), "invalid output options must fail before a generation request")
		})
	}
}

// Respond to the real executable's opt-in question in its synthetic PTY.
type imageTestDeclineWriter struct {
	buffer   bytes.Buffer
	answer   io.Writer
	answered bool
}

func (w *imageTestDeclineWriter) Bytes() []byte { return w.buffer.Bytes() }

func (w *imageTestDeclineWriter) Write(p []byte) (int, error) {
	n, err := w.buffer.Write(p)
	if err == nil && !w.answered && bytes.Contains(w.Bytes(), []byte("[y/N] ")) {
		w.answered = true
		_, err = io.WriteString(w.answer, "n\n")
	}
	return n, err
}
