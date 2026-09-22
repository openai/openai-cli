package custom

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
	assertSavedImage := func(t *testing.T, output string) {
		t.Helper()
		require.Equal(t, 1, strings.Count(output, "Saved image: "), output)
		require.NotContains(t, output, encodedImage)
		require.NotContains(t, output, "b64_json")
		for _, line := range strings.Split(output, "\n") {
			if quoted, ok := strings.CutPrefix(line, "Saved image: "); ok {
				path, err := strconv.Unquote(quoted)
				require.NoError(t, err)
				require.True(t, strings.HasSuffix(path, filepath.Join("Downloads", "gpt-images", "red-pixel.png")), path)
				saved, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, pngBytes, saved)
				files, err := os.ReadDir(filepath.Dir(path))
				require.NoError(t, err)
				require.Len(t, files, 1, "only the final image should remain")
				return
			}
		}
		t.Fatal("saved image path is missing")
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
	assertSavingPreset := func(t *testing.T, body map[string]any, wantPreset bool) {
		t.Helper()
		for key, value := range map[string]any{
			"n": float64(1), "size": "auto", "quality": "auto", "output_format": "png",
			"background": "auto", "moderation": "auto", "partial_images": float64(0), "stream": false,
		} {
			if wantPreset {
				require.Equal(t, value, body[key], key)
			} else {
				require.NotContains(t, body, key, "explicit models and API output retain API defaults")
			}
		}
		for _, key := range []string{"output_compression", "style", "user"} {
			require.NotContains(t, body, key, "specialized settings must stay optional")
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
		body := readRequest(t, requests)
		require.Equal(t, defaultSavedImageModel, body["model"])
		assertSavingPreset(t, body, true)
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
		{"TTY explicit uppercase JSON", "ghostty", "", []string{"--format", "JSON"}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, requests, count := newServer(t)
			output, runErr := runWithTerminal(t, server.URL, "", test.rootFlags, append([]string{"--prompt", "A red pixel"}, test.flags...), test.terminal)
			require.NoError(t, runErr, "PTY command failed")
			require.EqualValues(t, 1, count.Load())
			body := readRequest(t, requests)
			assertSavingPreset(t, body, test.rootFlags == nil)
			if test.rootFlags != nil {
				require.NotContains(t, body, "model")
				require.Contains(t, output, encodedImage)
				require.NotContains(t, output, "Saved image:")
				require.NotContains(t, output, "Generating image")
			} else {
				require.Equal(t, defaultSavedImageModel, body["model"])
				require.Contains(t, output, "Saved image:")
				require.Equal(t, 1, strings.Count(output, "Generating image..."))
				require.Less(t, strings.Index(output, "Generating image..."), strings.Index(output, "Saved image:"))
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
		preset                             bool
	}{
		{name: "default save model", flags: []string{"--prompt", "A red pixel"}, model: "gpt-image-2.5-sunburst", preset: true},
		{name: "no preview is CLI only", flags: []string{"--prompt", "A red pixel", "--no-preview"}, model: "gpt-image-2.5-sunburst", preset: true},
		{name: "inline off is CLI only", flags: []string{"--prompt", "A red pixel", "--inline", "off"}, model: "gpt-image-2.5-sunburst", preset: true},
		{name: "named image", flags: []string{"--prompt", "A red pixel", "--name", "orange-robot"}, model: "gpt-image-2.5-sunburst", preset: true},
		{name: "named image with extension", flags: []string{"--prompt", "A red pixel", "--name", "orange-robot.PNG"}, model: "gpt-image-2.5-sunburst", preset: true},
		{name: "explicit default model keeps API defaults", flags: []string{"--prompt", "A red pixel", "--model", "gpt-image-2.5-sunburst"}, model: "gpt-image-2.5-sunburst"},
		{name: "explicit model", flags: []string{"--prompt", "A red pixel", "--model", "gpt-image-1.5"}, model: "gpt-image-1.5"},
		{name: "documented alternate model", flags: []string{"--prompt", "A red pixel", "--model", "gpt-image-2.5-flare"}, model: "gpt-image-2.5-flare"},
		{name: "unknown model keeps API defaults", flags: []string{"--prompt", "A red pixel", "--model", "future-image-model"}, model: "future-image-model"},
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
			assertSavingPreset(t, body, test.preset)
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
			if strings.HasPrefix(test.name, "named image") {
				require.Equal(t, "orange-robot.png", files[0].Name())
			} else {
				require.Equal(t, "red-pixel.png", files[0].Name())
			}
			require.True(t, strings.Contains(output, savedPath) || strings.Contains(output, strconv.Quote(savedPath)), "output must identify the saved path: %s", output)
			require.NotContains(t, output, encodedImage)
		})
	}

	for _, test := range []struct {
		name, stdin, wantPrompt, wantName string
		flags                             []string
	}{
		{"prompt name from flag", "", "A tiny orange robot", "tiny-orange-robot.png", []string{"--prompt", "A tiny orange robot"}},
		{"prompt name from stdin", `{"prompt":"A tiny orange robot"}`, "A tiny orange robot", "tiny-orange-robot.png", nil},
		{"prompt flag overrides stdin name", `{"prompt":"A blue robot"}`, "The orange robot", "orange-robot.png", []string{"--prompt", "The orange robot"}},
		{"explicit name overrides prompt", `{"prompt":"A tiny orange robot"}`, "A tiny orange robot", "my-robot.png", []string{"--name", "my-robot.PNG"}},
		{"prompt without usable words", "", "🤖", "", []string{"--prompt", "🤖"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, requests, count := newServer(t)
			directory := t.TempDir()
			flags := append([]string{"--output-dir", directory}, test.flags...)
			output, err := run(t, server.URL, test.stdin, nil, flags)
			require.NoError(t, err, output)
			require.EqualValues(t, 1, count.Load(), "naming must not make another API request")
			body := readRequest(t, requests)
			require.Equal(t, test.wantPrompt, body["prompt"], "naming must not rewrite the generation prompt")
			files, err := os.ReadDir(directory)
			require.NoError(t, err)
			require.Len(t, files, 1)
			if test.wantName == "" {
				require.Regexp(t, `^image-\d{4}-\d{2}-\d{2}-\d{6}\.png$`, files[0].Name())
			} else {
				require.Equal(t, test.wantName, files[0].Name())
			}
		})
	}
	t.Run("prompt name collision keeps earlier file", func(t *testing.T) {
		server, requests, count := newServer(t)
		directory := t.TempDir()
		original := filepath.Join(directory, "tiny-orange-robot.png")
		require.NoError(t, os.WriteFile(original, []byte("existing synthetic file"), 0600))
		output, err := run(t, server.URL, "", nil, []string{"--output-dir", directory, "--prompt", "A tiny orange robot"})
		require.NoError(t, err, output)
		require.EqualValues(t, 1, count.Load())
		readRequest(t, requests)
		before, err := os.ReadFile(original)
		require.NoError(t, err)
		require.Equal(t, "existing synthetic file", string(before))
		after, err := os.ReadFile(filepath.Join(directory, "tiny-orange-robot-2.png"))
		require.NoError(t, err)
		require.Equal(t, pngBytes, after)
		require.Contains(t, output, "tiny-orange-robot-2.png")
	})

	for _, test := range []struct {
		name, stdin, want string
		flags             []string
	}{
		{
			name:  "preset flags override defaults",
			flags: []string{"--prompt", "A red pixel", "-n", "2", "--size", "1536x1024", "--quality", "low", "--output-format", "webp"},
			want:  `{"prompt":"A red pixel","model":"gpt-image-2.5-sunburst","n":2,"size":"1536x1024","quality":"low","output_format":"webp","background":"auto","moderation":"auto","partial_images":0,"stream":false}`,
		},
		{
			name:  "preset stdin overrides defaults",
			stdin: `{"prompt":"A red pixel","n":2,"size":"1024x1536","quality":"high","output_format":"jpeg"}`,
			want:  `{"prompt":"A red pixel","model":"gpt-image-2.5-sunburst","n":2,"size":"1024x1536","quality":"high","output_format":"jpeg","background":"auto","moderation":"auto","partial_images":0,"stream":false}`,
		},
		{
			name:  "preset flags override stdin",
			stdin: `{"prompt":"A red pixel","n":2,"size":"1024x1536","quality":"high","output_format":"jpeg"}`,
			flags: []string{"-n", "1", "--size", "1024x1024", "--quality", "low", "--output-format", "webp"},
			want:  `{"prompt":"A red pixel","model":"gpt-image-2.5-sunburst","n":1,"size":"1024x1024","quality":"low","output_format":"webp","background":"auto","moderation":"auto","partial_images":0,"stream":false}`,
		},
		{
			name:  "preset explicit nulls survive",
			stdin: `{"prompt":"A red pixel","n":null,"size":null,"quality":null,"output_format":null,"background":null,"moderation":null,"partial_images":null,"stream":null}`,
			want:  `{"prompt":"A red pixel","model":"gpt-image-2.5-sunburst","n":null,"size":null,"quality":null,"output_format":null,"background":null,"moderation":null,"partial_images":null,"stream":null}`,
		},
		{
			name:  "preset advanced flags override defaults",
			flags: []string{"--prompt", "A red pixel", "--background", "transparent", "--moderation", "low", "--partial-images", "0", "--stream", "false"},
			want:  `{"prompt":"A red pixel","model":"gpt-image-2.5-sunburst","n":1,"size":"auto","quality":"auto","output_format":"png","background":"transparent","moderation":"low","partial_images":0,"stream":false}`,
		},
		{
			name:  "preset advanced stdin flags merge",
			stdin: `{"prompt":"A red pixel","background":"transparent","moderation":"low","partial_images":null,"stream":null}`,
			flags: []string{"--background", "opaque", "--moderation", "auto"},
			want:  `{"prompt":"A red pixel","model":"gpt-image-2.5-sunburst","n":1,"size":"auto","quality":"auto","output_format":"png","background":"opaque","moderation":"auto","partial_images":null,"stream":null}`,
		},
		{
			name:  "null model keeps API defaults",
			stdin: `{"prompt":"A red pixel","model":null}`,
			want:  `{"prompt":"A red pixel","model":null}`,
		},
		{
			name:  "legacy response format keeps API defaults",
			flags: []string{"--prompt", "A red pixel", "--response-format", "b64_json"},
			want:  `{"prompt":"A red pixel","response_format":"b64_json"}`,
		},
		{
			name:  "null response format keeps API defaults",
			stdin: `{"prompt":"A red pixel","response_format":null}`,
			want:  `{"prompt":"A red pixel","response_format":null}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, requests, count := newServer(t)
			flags := append([]string{"--output-dir", t.TempDir()}, test.flags...)
			output, runErr := run(t, server.URL, test.stdin, nil, flags)
			require.NoError(t, runErr, output)
			require.EqualValues(t, 1, count.Load())
			body, err := json.Marshal(readRequest(t, requests))
			require.NoError(t, err)
			require.JSONEq(t, test.want, string(body))
			require.Contains(t, output, "Saved image:")
		})
	}

	for _, test := range []struct {
		name      string
		rootFlags []string
	}{
		{name: "nonterminal default saves image"},
		{name: "nonterminal explicit auto saves image", rootFlags: []string{"--format", "auto"}},
		{name: "nonterminal explicit text saves image", rootFlags: []string{"--format", "text"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, requests, calls := newServer(t)
			output, runErr := run(t, server.URL, "", test.rootFlags, []string{"--prompt", "A red pixel"})
			require.NoError(t, runErr, output)
			require.EqualValues(t, 1, calls.Load())
			body := readRequest(t, requests)
			require.Equal(t, defaultSavedImageModel, body["model"])
			assertSavingPreset(t, body, true)
			require.NotContains(t, output, "Generating image")
			assertSavedImage(t, output)
		})
	}

	for _, test := range []struct {
		name      string
		rootFlags []string
	}{
		{name: "explicit JSON", rootFlags: []string{"--format", "json"}},
		{name: "explicit JSONL", rootFlags: []string{"--format", "jsonl"}},
		{name: "explicit raw", rootFlags: []string{"--format", "raw"}},
		{name: "explicit pretty", rootFlags: []string{"--format", "pretty"}},
		{name: "explicit YAML", rootFlags: []string{"--format", "yaml"}},
		{name: "raw transform", rootFlags: []string{"--transform", "data.0.b64_json", "--raw-output"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, requests, _ := newServer(t)
			output, runErr := run(t, server.URL, "", test.rootFlags, []string{"--prompt", "A red pixel"})
			require.NoError(t, runErr, output)
			body := readRequest(t, requests)
			require.NotContains(t, body, "model", "ordinary API output must retain server model selection")
			assertSavingPreset(t, body, false)
			require.NotContains(t, output, "Generating image")
			require.NotContains(t, output, "Saved image")
			switch test.name {
			case "explicit pretty":
				require.Contains(t, output, "created: 123")
				require.Contains(t, output, "b64_json: "+encodedImage[:24])
			case "explicit YAML":
				require.Contains(t, output, "b64_json:")
				require.Contains(t, output, encodedImage)
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
		{name: "empty inline", flags: []string{"--inline", ""}},
		{name: "conflicting preview flags", flags: []string{"--inline", "on", "--no-preview"}},
		{name: "name path traversal", flags: []string{"--name", "../robot"}},
		{name: "empty name", flags: []string{"--name", ""}},
		{name: "filename too long", flags: []string{"--name", strings.Repeat("x", 512)}},
		{name: "JSON conflicts", rootFlags: []string{"--format", "json"}},
		{name: "YAML conflicts", rootFlags: []string{"--format", "yaml"}},
		{name: "transform conflicts", rootFlags: []string{"--transform", "data"}},
		{name: "raw output conflicts", rootFlags: []string{"--raw-output"}},
		{name: "saved stream event limit rejected", flags: []string{"--stream", "true", "--max-items", "1"}},
		{name: "saved partial event limit rejected", flags: []string{"--partial-images", "2", "--max-items", "1"}},
		{name: "partial stream false rejected", flags: []string{"--partial-images", "1", "--stream", "false"}},
		{name: "piped partial stream null rejected", stdin: `{"partial_images":1,"stream":null}`},
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
			require.NotContains(t, output, "Generating image")
		})
	}

	for _, test := range []struct {
		name, stdin, message string
		flags                []string
	}{
		{name: "settings zero count", flags: []string{"-n", "0"}, message: "--count (-n) must be a whole number from 1 to 10"},
		{name: "settings count alias too many", flags: []string{"--count", "11"}, message: "--count (-n) must be a whole number from 1 to 10"},
		{name: "settings too many images", flags: []string{"-n", "11"}, message: "--count (-n) must be a whole number from 1 to 10"},
		{name: "settings stdin count", stdin: `{"n":0}`, message: "--count (-n) must be a whole number from 1 to 10"},
		{name: "settings stdin stream string rejected", stdin: `{"stream":"true","n":2}`, message: "--stream must be true or false"},
		{name: "settings DALL-E3 count", flags: []string{"--model", "dall-e-3", "-n", "2"}, message: "dall-e-3 supports exactly one image"},
		{name: "settings merged DALL-E3", stdin: `{"model":"gpt-image-2.5-sunburst","n":2}`, flags: []string{"--model", "dall-e-3"}, message: "dall-e-3 supports exactly one image"},
		{name: "settings partial count", flags: []string{"--partial-images", "4"}, message: "--partial-images must be a whole number from 0 to 3"},
		{name: "settings partial requires one image", flags: []string{"--partial-images", "1", "-n", "2"}, message: "streaming and partial images support exactly one image"},
		{name: "settings stdin partial count", stdin: `{"partial_images":2,"n":2}`, message: "streaming and partial images support exactly one image"},
		{name: "settings streamed count", flags: []string{"--stream", "true", "-n", "2"}, message: "streaming and partial images support exactly one image"},
		{name: "settings transparent JPEG", flags: []string{"--background", "transparent", "--output-format", "jpeg"}, message: "JPEG does not support transparent backgrounds"},
		{name: "settings merged transparent JPEG", stdin: `{"background":"opaque","output_format":"jpeg"}`, flags: []string{"--background", "transparent"}, message: "JPEG does not support transparent backgrounds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, _, count := newServer(t)
			// Validation must run before directory preflight as well as the API.
			missingDirectory := filepath.Join(t.TempDir(), "not-created")
			flags := append([]string{"--prompt", "A red pixel", "--output-dir", missingDirectory}, test.flags...)
			output, runErr := run(t, server.URL, test.stdin, nil, flags)
			require.Error(t, runErr)
			require.Contains(t, output, test.message)
			require.Zero(t, count.Load())
			_, statErr := os.Stat(missingDirectory)
			require.ErrorIs(t, statErr, os.ErrNotExist)
			require.NotContains(t, output, "Generating image")
		})
	}

	for _, test := range []struct {
		name, stdin string
		flags       []string
	}{
		{name: "legacy URL flags retain nonstreaming API output", flags: []string{"--response-format", "url", "--partial-images", "1", "--stream", "false"}},
		{name: "legacy URL YAML retains nonstreaming API output", stdin: "response_format: url\npartial_images: 1\nstream: false\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := make(chan request, 1)
			var calls atomic.Int32
			const urlResponse = `{"created":123,"data":[{"url":"https://images.example.invalid/synthetic.png"}]}`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, "cannot read synthetic request", http.StatusBadRequest)
					return
				}
				requests <- request{method: r.Method, path: r.URL.Path, body: body}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, urlResponse)
			}))
			defer server.Close()
			flags := append([]string{"--prompt", "A red pixel", "--model", "gpt-image-2"}, test.flags...)
			output, runErr := run(t, server.URL, test.stdin, []string{"--format", "json"}, flags)
			require.NoError(t, runErr, output)
			require.EqualValues(t, 1, calls.Load())
			body, err := json.Marshal(readRequest(t, requests))
			require.NoError(t, err)
			require.JSONEq(t, `{"prompt":"A red pixel","model":"gpt-image-2","response_format":"url","partial_images":1,"stream":false}`, string(body))
			require.JSONEq(t, urlResponse, output)
		})
	}

	t.Run("settings API mode validates count before network", func(t *testing.T) {
		server, _, count := newServer(t)
		output, runErr := run(t, server.URL, `{"n":11}`, []string{"--format", "json"}, []string{"--prompt", "A red pixel"})
		require.Error(t, runErr)
		require.Contains(t, output, "--count (-n) must be a whole number from 1 to 10")
		require.Zero(t, count.Load())
	})

	for _, test := range []struct {
		name, stdin      string
		rootFlags, flags []string
		message          string
	}{
		{name: "settings API partials need explicit streaming", rootFlags: []string{"--format", "json"}, flags: []string{"--partial-images", "2"}, message: "--partial-images needs --stream true for API output"},
		{name: "settings piped API partials need explicit streaming", stdin: `{"partial_images":2}`, rootFlags: []string{"--format", "json"}, message: "--partial-images needs --stream true for API output"},
		{name: "settings explicit false partial streaming stays false", stdin: `{"partial_images":2,"stream":false}`, message: "--partial-images needs streaming"},
		{name: "settings explicit null partial streaming stays null", stdin: `{"partial_images":2,"stream":null}`, message: "--partial-images needs streaming"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, _, count := newServer(t)
			flags := append([]string{"--prompt", "A red pixel"}, test.flags...)
			output, runErr := run(t, server.URL, test.stdin, test.rootFlags, flags)
			require.Error(t, runErr)
			require.Contains(t, output, test.message)
			require.Zero(t, count.Load())
		})
	}

	for _, test := range []struct {
		name, stdin string
		count       int
	}{
		{name: "settings count alias minimum", count: 1},
		{name: "settings count alias maximum", count: 10},
		{name: "settings count alias overrides stdin", stdin: `{"n":0}`, count: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, requests, calls := newServer(t)
			flags := []string{"--prompt", "A red pixel", "--count", strconv.Itoa(test.count)}
			output, runErr := run(t, server.URL, test.stdin, nil, flags)
			require.NoError(t, runErr, output)
			require.EqualValues(t, 1, calls.Load())
			body := readRequest(t, requests)
			require.EqualValues(t, test.count, body["n"])
			require.NotContains(t, body, "count", "alias must serialize as the existing API field n")
		})
	}

	for _, test := range []struct {
		name, stdin, model, format string
		flags                      []string
	}{
		{name: "settings final count flag overrides bad stdin", stdin: `{"n":0}`, flags: []string{"-n", "2"}},
		{name: "settings model flag resolves DALL-E3 limit", stdin: `{"model":"dall-e-3","n":2}`, flags: []string{"--model", "gpt-image-2.5-sunburst"}, model: "gpt-image-2.5-sunburst"},
		{name: "settings format flag resolves transparency", stdin: `{"background":"transparent","output_format":"jpeg","n":2}`, flags: []string{"--output-format", "webp"}, format: "webp"},
		{name: "settings future model options pass through", flags: []string{"--model", "future-image-model", "-n", "2", "--size", "future-size", "--quality", "future-quality"}, model: "future-image-model"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, requests, count := newServer(t)
			flags := append([]string{"--prompt", "A red pixel"}, test.flags...)
			output, runErr := run(t, server.URL, test.stdin, nil, flags)
			require.NoError(t, runErr, output)
			require.EqualValues(t, 1, count.Load())
			body := readRequest(t, requests)
			require.EqualValues(t, 2, body["n"])
			if test.model != "" {
				require.Equal(t, test.model, body["model"])
			}
			if test.format != "" {
				require.Equal(t, test.format, body["output_format"])
			}
		})
	}

	for _, test := range []struct {
		name, stdin string
		flags       []string
		format      string
		partials    int64
	}{
		{name: "settings partial images with explicit stream save final", stdin: `{"partial_images":2}`, flags: []string{"--stream", "true"}, partials: 2},
		{name: "settings merged stdin selects saving stream decoder", stdin: `{"partial_images":2,"stream":true}`, partials: 2},
		{name: "settings piped partials enable saved streaming", stdin: `{"partial_images":2}`, partials: 2},
		{name: "bare stream saves final", flags: []string{"--stream", "true"}},
		{name: "explicit JSON stream keeps API events", stdin: `{"partial_images":2}`, flags: []string{"--stream", "true"}, format: "json", partials: 2},
		{name: "explicit JSONL merged stdin selects API stream decoder", stdin: `{"partial_images":2,"stream":true}`, format: "jsonl", partials: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := make(chan request, 1)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, readErr := io.ReadAll(r.Body)
				if readErr != nil {
					http.Error(w, "cannot read synthetic request", http.StatusBadRequest)
					return
				}
				requests <- request{method: r.Method, path: r.URL.Path, body: body}
				w.Header().Set("Content-Type", "text/event-stream")
				if test.partials > 0 {
					_, _ = io.WriteString(w, "data: {\"type\":\"image_generation.partial_image\",\"partial_image_index\":0,\"b64_json\":\""+encodedImage+"\"}\n\n")
				}
				_, _ = io.WriteString(w, "data: {\"type\":\"image_generation.completed\",\"b64_json\":\""+encodedImage+"\"}\n\n")
			}))
			defer server.Close()
			flags := append([]string{"--prompt", "A red pixel", "--model", "gpt-image-2.5-sunburst"}, test.flags...)
			var rootFlags []string
			if test.format != "" {
				rootFlags = []string{"--format", test.format}
			}
			output, runErr := run(t, server.URL, test.stdin, rootFlags, flags)
			require.NoError(t, runErr, output)
			require.EqualValues(t, 1, calls.Load())
			body := readRequest(t, requests)
			if test.partials > 0 {
				require.EqualValues(t, test.partials, body["partial_images"])
			} else {
				require.NotContains(t, body, "partial_images", "explicit model keeps omitted API options")
			}
			require.Equal(t, true, body["stream"])
			if test.format != "" {
				require.Contains(t, output, "image_generation.partial_image")
				require.Contains(t, output, "image_generation.completed")
				require.Contains(t, output, encodedImage)
				require.NotContains(t, output, "Saved image")
			} else {
				assertSavedImage(t, output)
				require.NotContains(t, output, "image_generation.completed")
			}
		})
	}

	t.Run("partial batch keeps and reports every completed image", func(t *testing.T) {
		count := new(atomic.Int32)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{
				{"b64_json": encodedImage}, {"b64_json": "not-an-image"}, {"b64_json": encodedImage},
			}})
		}))
		defer server.Close()
		directory := filepath.Join(t.TempDir(), "my images")
		require.NoError(t, os.Mkdir(directory, 0700))
		output, runErr := run(t, server.URL, "", nil, []string{"--prompt", "A red pixel", "-n", "3", "--name", "batch.png", "--output-dir", directory, "--inline", "off"})
		require.Error(t, runErr)
		require.EqualValues(t, 1, count.Load(), "saving a partial batch must not retry generation")
		files, err := os.ReadDir(directory)
		require.NoError(t, err)
		require.Len(t, files, 2)
		for _, name := range []string{"batch.png", "batch-2.png"} {
			path := filepath.Join(directory, name)
			saved, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, pngBytes, saved)
			require.Contains(t, output, "Saved image: "+strconv.Quote(path))
		}
		require.Contains(t, output, "The files listed above are saved")
		require.NotContains(t, output, encodedImage)
	})
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
