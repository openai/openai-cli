package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/charmbracelet/x/term"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMainInlineImagePipeAndAPIOutput(t *testing.T) {
	payload := imageGenerationPNG(t)
	for _, tc := range []struct {
		name  string
		flags []string
		json  bool
	}{
		{"automatic pipe", nil, false},
		{"on cannot force pipe", []string{"--inline", "on"}, false},
		{"off", []string{"--inline", "off"}, false},
		{"explicit API format", []string{"--format", "json", "--inline", "on"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var body map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				require.NotContains(t, body, "inline")
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, imageGenerationResponse(payload))
			}))
			defer server.Close()
			home := t.TempDir()
			env := append(imageGenerationEnv(server, home), "TERM=xterm-kitty", "TERM_PROGRAM=kitty", "CI=false")
			args := append([]string{"openai", "images", "generate", "--prompt", "synthetic preview"}, tc.flags...)
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			require.Zero(t, got.code, got.stderr)
			require.Equal(t, int32(1), calls.Load())
			require.Empty(t, got.stderr)
			require.NotContains(t, got.stdout, "\x1b")
			require.NotContains(t, got.stdout, "Inline preview")
			if tc.json {
				require.True(t, json.Valid([]byte(got.stdout)))
				require.Empty(t, imageGenerationFiles(t, filepath.Join(home, "Downloads", "gpt-images")))
			} else {
				assertImageGenerationFiles(t, filepath.Join(home, "Downloads", "gpt-images"), got.stdout, 1, payload)
			}
		})
	}
}

func TestMainInlineImageInvalidModeBeforeRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	for _, command := range []string{"generate", "edit", "create-variation"} {
		got := runMainDispatchWithEnv(t, "bash", imageGenerationEnv(server, t.TempDir()), "openai", "images", command, "--inline", "not-a-mode")
		require.Equal(t, 1, got.code)
		require.Contains(t, got.stderr, "--inline must be auto, on or off")
		require.Empty(t, strings.TrimSpace(got.stdout))
	}
	require.Zero(t, calls.Load())
}

func TestMainInlineSavedImageWarningsOnTerminal(t *testing.T) {
	if !term.IsTerminal(os.Stdout.Fd()) {
		t.Skip("requires actual terminal stdout")
	}
	var tall bytes.Buffer
	require.NoError(t, png.Encode(&tall, image.NewNRGBA(image.Rect(0, 0, 1, 16384))))
	for _, tc := range []struct {
		name       string
		payload    []byte
		diagnostic string
	}{
		{"invalid optional preview", append([]byte("\x89PNG\r\n\x1a\n"), []byte("synthetic invalid preview")...), "No need to generate again"},
		{"too tall", tall.Bytes(), "too tall"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, imageGenerationResponse(tc.payload))
			}))
			defer server.Close()
			binary, err := os.Executable()
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, binary, "-test.run=^TestMainDispatchProcess$", "--", "openai", "images", "generate", "--prompt", "synthetic preview warning")
			home := t.TempDir()
			child.Env = append(imageGenerationEnv(server, home), "OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "TERM=xterm-256color", "TERM_PROGRAM=synthetic-test-pty", "CI=false")
			var diagnostic bytes.Buffer
			child.Stdout = os.Stdout
			child.Stderr = &diagnostic
			require.NoError(t, child.Run(), diagnostic.String())
			require.NoError(t, ctx.Err())
			require.Contains(t, diagnostic.String(), tc.diagnostic)
			files := imageGenerationFiles(t, filepath.Join(home, "Downloads", "gpt-images"))
			require.Len(t, files, 1)
			saved, err := os.ReadFile(files[0])
			require.NoError(t, err)
			require.Equal(t, tc.payload, saved)
		})
	}
}
