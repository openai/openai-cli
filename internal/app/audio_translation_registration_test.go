package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openai/openai-cli/generated/commands"
	"github.com/openai/openai-cli/internal/app"
	"github.com/openai/openai-cli/internal/audiolive"
	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

const audioTranslationCaseEnv = "OPENAI_TEST_AUDIO_TRANSLATION_REGISTRATION_CASE"

// Generated flags retain parse state. Each case needs its own command tree and
// process stdin, rather than another invocation of the generated singletons.
func TestAudioTranslationGeneratedRegistration(t *testing.T) {
	if name := os.Getenv(audioTranslationCaseEnv); name != "" {
		testAudioTranslationGeneratedCase(t, name)
		return
	}
	executable, err := os.Executable()
	require.NoError(t, err)
	for _, name := range []string{
		"live alone", "live preserves pipe", "live with file", "live with model",
		"missing both", "missing file", "missing model",
		"file flags", "file JSON", "file YAML", "JSON missing file", "YAML missing model",
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, executable, "-test.run=^TestAudioTranslationGeneratedRegistration$", "-test.count=1")
			child.WaitDelay = time.Second
			for _, value := range os.Environ() {
				key, _, _ := strings.Cut(value, "=")
				key = strings.ToUpper(key)
				if strings.HasPrefix(key, "OPENAI_") || strings.HasPrefix(key, "TEST_API_") {
					continue
				}
				switch key {
				case "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
					continue
				}
				child.Env = append(child.Env, value)
			}
			home := t.TempDir()
			child.Env = append(child.Env, audioTranslationCaseEnv+"="+name,
				"HOME="+home, "USERPROFILE="+home, "APPDATA="+home, "LOCALAPPDATA="+home,
				"XDG_CONFIG_HOME="+home, "XDG_CACHE_HOME="+home,
				"OPENAI_UNTRUSTED_STDIN=false")
			output, err := child.CombinedOutput()
			require.NoError(t, ctx.Err(), "registration helper timed out: %s", output)
			require.NoError(t, err, "registration helper: %s", output)
		})
	}
}

func testAudioTranslationGeneratedCase(t *testing.T, name string) {
	const audio = "synthetic audio upload bytes"
	const liveInput = "This is piped text, not a translation request.\n"
	path := filepath.Join(t.TempDir(), "synthetic recording.wav")
	require.NoError(t, os.WriteFile(path, []byte(audio), 0o600))
	jsonBody, err := json.Marshal(map[string]string{"file": path, "model": "whisper-1"})
	require.NoError(t, err)
	type testCase struct {
		args    []string
		input   string
		missing string
		live    bool
		mixed   bool
	}
	cases := map[string]testCase{
		"live alone":          {args: []string{"--live"}, live: true},
		"live preserves pipe": {args: []string{"--live"}, input: liveInput, live: true},
		"live with file":      {args: []string{"--live", "--file", path}, live: true, mixed: true},
		"live with model":     {args: []string{"--live", "--model", "whisper-1"}, live: true, mixed: true},
		"missing both":        {missing: `Required flags "file, model" not set`},
		"missing file":        {args: []string{"--model", "whisper-1"}, missing: `Required flag "file" not set`},
		"missing model":       {args: []string{"--file", path}, missing: `Required flag "model" not set`},
		"file flags":          {args: []string{"--file", path, "--model", "whisper-1"}},
		"file JSON":           {input: string(jsonBody)},
		"file YAML":           {input: fmt.Sprintf("file: %q\nmodel: whisper-1\n", path)},
		"JSON missing file":   {input: `{"model":"whisper-1"}`, missing: `Required flag "file" not set`},
		"YAML missing model":  {input: fmt.Sprintf("file: %q\n", path), missing: `Required flag "model" not set`},
	}
	tc, ok := cases[name]
	require.True(t, ok, "unknown helper case %q", name)

	input, writer, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, input.Close()) })
	_, err = io.WriteString(writer, tc.input)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	previousStdin := os.Stdin
	os.Stdin = input
	t.Cleanup(func() { os.Stdin = previousStdin })

	type request struct {
		method, path, authorization, model, filename, audio, live string
		err                                                       error
	}
	var mu sync.Mutex
	var requests []request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := request{method: r.Method, path: r.URL.Path, authorization: r.Header.Get("Authorization")}
		got.err = r.ParseMultipartForm(1 << 20)
		if got.err == nil {
			defer r.MultipartForm.RemoveAll()
			got.model, got.live = r.FormValue("model"), r.FormValue("live")
			file, header, openErr := r.FormFile("file")
			got.err = openErr
			if openErr == nil {
				data, readErr := io.ReadAll(file)
				closeErr := file.Close()
				got.filename, got.audio, got.err = header.Filename, string(data), readErr
				if got.err == nil {
					got.err = closeErr
				}
			}
		}
		mu.Lock()
		requests = append(requests, got)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":"The train leaves at five."}`)
	}))
	t.Cleanup(server.Close)

	root := localcommands.CloneResourceCommand(commands.Command)
	app.ConfigureCommand(root)
	var stdout, stderr bytes.Buffer
	root.Writer, root.ErrWriter = &stdout, &stderr
	if tc.live {
		before := root.Before
		root.Before = func(ctx context.Context, command *cli.Command) (context.Context, error) {
			ctx, err := before(ctx, command)
			if err == nil {
				// Reach audiolive.Run's configuration guard without opening its
				// listener, launching a browser, or connecting to a live API.
				root.Writer = nil
			}
			return ctx, err
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	args := []string{"openai", "--base-url", server.URL + "/v1/", "--api-key", "synthetic-translation-key", "audio", "translate"}
	err = root.Run(ctx, append(args, tc.args...))
	if tc.mixed {
		require.ErrorContains(t, err, "Use --live by itself")
	} else if tc.live {
		var failure audiolive.Failure
		require.ErrorAs(t, err, &failure)
		require.Equal(t, audiolive.Failure("Live translation could not start: browser or API configuration is missing."), failure)
	} else if tc.missing != "" {
		require.ErrorContains(t, err, tc.missing)
	} else {
		require.NoError(t, err, "stdout: %s\nstderr: %s", stdout.String(), stderr.String())
	}
	if tc.live {
		remaining, readErr := io.ReadAll(input)
		require.NoError(t, readErr)
		require.Equal(t, tc.input, string(remaining), "live mode consumed piped input")
	}
	mu.Lock()
	defer mu.Unlock()
	if tc.live || tc.missing != "" {
		require.Empty(t, requests, "invalid or live invocation sent a file translation request")
		return
	}
	require.Len(t, requests, 1)
	got := requests[0]
	require.NoError(t, got.err)
	require.Equal(t, http.MethodPost, got.method)
	require.Equal(t, "/v1/audio/translations", got.path)
	require.Equal(t, "Bearer synthetic-translation-key", got.authorization)
	require.Equal(t, "whisper-1", got.model)
	require.Equal(t, filepath.Base(path), got.filename)
	require.Equal(t, audio, got.audio)
	require.Empty(t, got.live, "local live flag leaked into the file translation body")
}
