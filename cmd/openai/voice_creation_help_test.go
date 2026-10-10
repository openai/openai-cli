package main

import (
	"bytes"
	"context"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
)

const voiceCreationResponse = `{"id":"voice_synthetic","object":"audio.voice","name":"Demo","created_at":17,"future":{"count":9007199254740993}}`

func TestMainVoiceCreationHelp(t *testing.T) {
	for _, args := range [][]string{
		{"audio", "voices", "create", "--help"},
		{"audio:voices", "create", "--help"},
		{"help", "audio", "voices", "create"},
		{"help", "audio:voices", "create"},
		{"audio", "voices", "help", "create"},
		{"--format", "json", "help", "audio", "voices", "create"},
		{"help", "audio", "voices", "create", "--format", "json"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=not a URL"}, append([]string{"openai"}, args...)...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("voice help attempted a request or failed: %+v", got)
			}
			text := strings.Join(strings.Fields(got.stdout), " ")
			for _, want := range []string{
				"Requires custom-voice access", "same speaker as the consent recording",
				"First upload the consent recording through the API", "Replace cons_demo with the returned consent ID",
				"Replace sample.wav with your sample recording", "https://developers.openai.com/api/docs/guides/custom-voices",
				"--audio-sample", "--consent", "--name", "--type", "GLOBAL OPTIONS:",
			} {
				if !strings.Contains(text, want) {
					t.Errorf("voice help lacks %q:\n%s", want, got.stdout)
				}
			}
			want := "openai audio voices create --name Demo --consent cons_demo --audio-sample sample.wav"
			if command := voiceCreationExample(t, got.stdout); command != want {
				t.Errorf("voice example = %q, want %q", command, want)
			}
			if strings.Contains(text, "--prompt") || strings.Contains(text, "from descriptions") {
				t.Errorf("voice help advertises unsupported description input: %s", text)
			}
		})
	}
	for _, args := range [][]string{{"audio", "--help"}, {"audio", "voices", "--help"}, {"audio:voices", "--help"}} {
		got := runMainDispatch(t, "bash", append([]string{"openai"}, args...)...)
		text := strings.Join(strings.Fields(got.stdout), " ")
		if got.code != 0 || got.stderr != "" || !strings.Contains(text, "Create a voice from an audio sample and consent.") || strings.Contains(text, "from descriptions") {
			t.Fatalf("voice group description is inaccurate: %+v", got)
		}
	}
}

func TestMainVoiceCreationExamplePreservesRequestAndOutput(t *testing.T) {
	data := []byte("RIFF\x00synthetic sample\r\n\xffWAVE")
	file := filepath.Join(t.TempDir(), "sample with spaces.wav")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{
		"Authorization": "Bearer fake-voice-explicit-key", "OpenAI-Organization": "org_voice",
		"OpenAI-Project": "proj_voice", "X-Voice-Test": "synthetic",
	}
	server, requests := voiceCreationServer(t, filepath.Base(file), data, headers)
	for _, route := range [][]string{{"audio", "voices"}, {"audio:voices"}} {
		for _, format := range []string{"json", "jsonl", "yaml", "raw", "transform", "raw-output"} {
			t.Run(strings.Join(route, "/")+"/"+format, func(t *testing.T) {
				helpArgs := append(append([]string{"openai"}, route...), "create", "--help")
				help := runMainDispatch(t, "bash", helpArgs...)
				if help.code != 0 || help.stderr != "" {
					t.Fatalf("voice help failed: %+v", help)
				}
				args := strings.Fields(voiceCreationExample(t, help.stdout))
				// Keep the printed flags, but exercise each registered route.
				args = append(append([]string{"openai"}, route...), args[3:]...)
				for i, arg := range args {
					if arg == "sample.wav" {
						args[i] = file
					}
				}
				args = append(args, "--base-url", server.URL, "--api-key", "fake-voice-explicit-key",
					"--organization", "org_voice", "--project", "proj_voice", "--header", "X-Voice-Test: synthetic")
				switch format {
				case "transform":
					args = append(args, "--format", "json", "--transform", "id")
				case "raw-output":
					args = append(args, "--transform", "id", "--raw-output")
				default:
					args = append(args, "--format", format)
				}
				requests.Store(0)
				got := runMainDispatchWithEnv(t, "bash", []string{
					"OPENAI_API_KEY=fake-voice-environment-key", "OPENAI_ORG_ID=org_environment", "OPENAI_PROJECT_ID=proj_environment",
				}, args...)
				if got.code != 0 || got.stderr != "" || requests.Load() != 1 {
					t.Fatalf("copied voice example failed: result=%+v requests=%d", got, requests.Load())
				}
				switch format {
				case "raw-output":
					if got.stdout != "voice_synthetic\n" {
						t.Fatalf("raw extraction changed: %q", got.stdout)
					}
				case "transform":
					assertReadableAudioJSON(t, got.stdout, `"voice_synthetic"`)
				case "yaml":
					decoded, err := yaml.YAMLToJSON([]byte(got.stdout))
					if err != nil {
						t.Fatal(err)
					}
					assertReadableAudioJSON(t, string(decoded), voiceCreationResponse)
				default:
					assertReadableAudioJSON(t, got.stdout, voiceCreationResponse)
					if format == "jsonl" && strings.Count(got.stdout, "\n") != 1 {
						t.Fatalf("JSONL spans multiple lines: %q", got.stdout)
					}
					if format == "raw" && got.stdout != voiceCreationResponse+"\n" {
						t.Fatalf("raw response bytes changed: %q", got.stdout)
					}
				}
			})
		}
	}
	if after, err := os.ReadFile(file); err != nil || !bytes.Equal(after, data) {
		t.Fatalf("voice creation changed the sample: %v", err)
	}
}

func TestMainVoiceCreationPreservesErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sample.wav")
	if err := os.WriteFile(file, []byte("synthetic\x00sample"), 0o600); err != nil {
		t.Fatal(err)
	}
	const details = `{"message":"Synthetic consent rejection","type":"invalid_request_error","code":"invalid_consent","param":"consent"}`
	for _, route := range [][]string{{"audio", "voices"}, {"audio:voices"}} {
		args := append(slices.Clone(route), "create", "--name", "Demo", "--consent", "cons_demo", "--audio-sample", file)
		got := runMainCommandAPIErrorResponse(t, http.StatusForbidden, "application/json", `{"error":`+details+`}`, args, "--format", "json")
		if actual, want := decodeMainErrorObject(t, "json", got.stderr), decodeMainErrorObject(t, "json", details); !reflect.DeepEqual(actual, want) {
			t.Fatalf("voice API error changed: got %v, want %v", actual, want)
		}
		server, requests := voiceCreationServer(t, filepath.Base(file), []byte("synthetic\x00sample"), nil)
		args = append(append([]string{"openai", "--base-url", server.URL}, args...), "--prompt", "A described voice")
		got = runMainDispatchWithEnv(t, "bash", []string{"OPENAI_API_KEY=fake-voice-key"}, args...)
		if got.code != 1 || got.stdout != "" || !strings.Contains(got.stderr, "An option is not recognized.") ||
			!strings.Contains(got.stderr, "Options and examples:") || requests.Load() != 0 {
			t.Fatalf("unsupported prompt did not fail before the request: result=%+v requests=%d", got, requests.Load())
		}
	}
}

func TestMainVoiceCreationNativeHelpExample(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Bash/zsh invocation checks require a Unix host")
	}
	work := filepath.Join(t.TempDir(), "CLI with spaces & apostrophe's")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(work, "openai")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	invocation := "'" + strings.ReplaceAll(binary, "'", "'\\''") + "'"
	data := []byte("RIFF\x00synthetic sample\r\n\xffWAVE")
	server, requests := voiceCreationServer(t, "sample.wav", data, map[string]string{"Authorization": "Bearer fake-native-shell-key"})
	required := strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ",")
	for _, shell := range []nativeShell{
		{"bash", "bash", "", "export OPENAI_API_KEY='fake-native-shell-key'; ", []string{"--noprofile", "--norc", "-c"}},
		{"zsh", "zsh", "", "export OPENAI_API_KEY='fake-native-shell-key'; ", []string{"-f", "-c"}},
	} {
		t.Run(shell.name, func(t *testing.T) {
			path, err := exec.LookPath(shell.executable)
			if err != nil {
				if slices.Contains(required, shell.name) {
					t.Fatalf("required shell is unavailable: %v", err)
				}
				t.Skipf("%s is not installed", shell.name)
			}
			shell.executable = path
			for _, lookup := range []string{"absent", "unrelated", "installed"} {
				t.Run(lookup, func(t *testing.T) {
					directory, home, searchPath := t.TempDir(), t.TempDir(), t.TempDir()
					printedInvocation := invocation
					if lookup == "unrelated" {
						if err := os.WriteFile(filepath.Join(searchPath, "openai"), []byte("#!/bin/sh\nprintf 'wrong executable\\n'\nexit 99\n"), 0o700); err != nil {
							t.Fatal(err)
						}
					} else if lookup == "installed" {
						searchPath, printedInvocation = work, "openai"
					}
					t.Setenv("PATH", searchPath)
					if err := os.WriteFile(filepath.Join(directory, "sample.wav"), data, 0o600); err != nil {
						t.Fatal(err)
					}
					for _, topic := range []string{" audio voices create --help", " help audio:voices create"} {
						requests.Store(0)
						help := runNativeShell(t, shell, directory, home, server.URL, invocation+topic)
						if help.code != 0 || help.stderr != "" || requests.Load() != 0 {
							t.Fatalf("voice help failed or made a request: %+v", help)
						}
						command := voiceCreationExample(t, help.stdout)
						if !strings.HasPrefix(command, printedInvocation+" audio voices create ") {
							t.Fatalf("voice example changed executable identity: %q", command)
						}
						got := runNativeShell(t, shell, directory, home, server.URL, shell.setKey+command+" --format json")
						if got.code != 0 || got.stderr != "" || requests.Load() != 1 {
							t.Fatalf("copied voice example failed: result=%+v requests=%d", got, requests.Load())
						}
						assertReadableAudioJSON(t, got.stdout, voiceCreationResponse)
					}
				})
			}
		})
	}
}

func voiceCreationExample(t *testing.T, output string) string {
	t.Helper()
	_, examples, found := strings.Cut(output, "EXAMPLES:\n")
	if !found {
		t.Fatalf("voice help omitted examples: %s", output)
	}
	examples, _, _ = strings.Cut(examples, "\nOPTIONS:")
	var commands []string
	for line := range strings.SplitSeq(examples, "\n") {
		if strings.Contains(line, " audio voices create ") {
			commands = append(commands, strings.TrimSpace(line))
		}
	}
	if len(commands) != 1 {
		t.Fatalf("voice help needs one executable example: %s", examples)
	}
	return commands[0]
}

func voiceCreationServer(t *testing.T, filename string, sample []byte, headers map[string]string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/audio/voices" || r.URL.RawQuery != "" {
			t.Errorf("unexpected voice request: %s %s", r.Method, r.URL)
		}
		for name, want := range headers {
			if r.Header.Get(name) != want {
				t.Errorf("voice request did not preserve %s", name)
			}
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			http.Error(w, "synthetic multipart failure", http.StatusBadRequest)
			return
		}
		fields, files := make(map[string]string), 0
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Error(err)
				return
			}
			body, readErr := io.ReadAll(part)
			closeErr := part.Close()
			if readErr != nil || closeErr != nil {
				t.Errorf("read synthetic multipart: %v; close: %v", readErr, closeErr)
				return
			}
			if part.FormName() == "audio_sample" {
				files++
				if !bytes.Equal(body, sample) || part.FileName() != filename || part.Header.Get("Content-Type") != mime.TypeByExtension(filepath.Ext(filename)) {
					t.Error("voice upload changed sample bytes, filename, or MIME type")
				}
			} else {
				if _, duplicate := fields[part.FormName()]; duplicate {
					t.Errorf("duplicate multipart field %s", part.FormName())
				}
				fields[part.FormName()] = string(body)
			}
		}
		// Omitted --type stays absent on the wire; the API applies its default.
		if want := map[string]string{"name": "Demo", "consent": "cons_demo"}; files != 1 || !reflect.DeepEqual(fields, want) {
			t.Errorf("voice multipart changed: files=%d fields=%v", files, fields)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, voiceCreationResponse); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	return server, &requests
}
