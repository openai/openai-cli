package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

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
