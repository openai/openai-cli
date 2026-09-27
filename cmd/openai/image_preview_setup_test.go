package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/openai/openai-cli/internal/imageprefs"
	"github.com/stretchr/testify/require"
)

// These are real entrypoint checks with unsupported terminal output. They must
// not activate a native font or read the developer's Terminal session.
func TestMainImagePreviewSetupCommandsAreLocal(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected API request", http.StatusInternalServerError)
	}))
	defer server.Close()
	env, preferences := localImageTestEnvironment(t, server.URL)
	cache := t.TempDir()
	env = append(env, "XDG_CACHE_HOME="+cache, "LOCALAPPDATA="+cache, "TERM_SESSION_ID=")
	inputPath := filepath.Join(t.TempDir(), "unread-api-input.json")
	require.NoError(t, os.WriteFile(inputPath, []byte(`{"synthetic":"API input must remain unread"}`), 0600))
	input, err := os.Open(inputPath)
	require.NoError(t, err)
	defer func() { _ = input.Close() }()
	run := func(args ...string) mainDispatchResult {
		return runMainDispatchWithStdin(t, "bash", env, input, append([]string{"openai"}, args...)...)
	}
	got := run("images", "inline", "on")
	require.Zero(t, got.code, got.stderr)
	require.Empty(t, got.stderr)
	require.Contains(t, got.stdout, "Automatic image previews on")
	original, err := os.ReadFile(preferences)
	require.NoError(t, err)

	got = run("images", "inline", "--help")
	require.Zero(t, got.code, got.stderr)
	require.Empty(t, got.stderr)
	for _, command := range []string{"on", "off", "setup", "repair", "status"} {
		count := 0
		for line := range strings.SplitSeq(got.stdout, "\n") {
			if fields := strings.Fields(line); len(fields) > 0 && fields[0] == command {
				count++
			}
		}
		require.Equal(t, 1, count, "expected one %s command in %q", command, got.stdout)
	}
	for _, command := range []string{"setup", "repair", "status"} {
		t.Run(command, func(t *testing.T) {
			for _, flag := range []string{"-h", "--help", "--h"} {
				got := run("images", "inline", command, flag)
				require.Zero(t, got.code, got.stderr)
				require.Empty(t, got.stderr)
				require.Contains(t, got.stdout, "Automation permission")
				require.Contains(t, got.stdout, "preferences are unchanged")
				require.Contains(t, got.stdout, "No cache reset")
			}
			got := run("help", "--all", "images", "inline", command)
			require.Zero(t, got.code, got.stderr)
			require.Empty(t, got.stderr)
			require.Contains(t, got.stdout, "Automation permission")
			require.Contains(t, got.stdout, "automatic preview preferences are unchanged")
			got = run("images", "inline", command)
			if command == "status" {
				require.Zero(t, got.code, got.stderr)
				require.Empty(t, got.stderr)
				require.Contains(t, got.stdout, "Sharp preview setup is unavailable here")
			} else {
				require.Equal(t, 1, got.code)
				require.Empty(t, got.stdout)
				require.Contains(t, got.stderr, "local Apple Terminal tab")
			}
		})
	}
	current, err := os.ReadFile(preferences)
	require.NoError(t, err)
	require.Equal(t, original, current, "help, status and failed recovery changed the saved preference")
	got = run("images", "inline", "off")
	require.Zero(t, got.code, got.stderr)
	require.Empty(t, got.stderr)
	mode, err := imageprefs.Load(t.Context(), preferences)
	require.NoError(t, err)
	require.Equal(t, "off", mode)
	position, err := input.Seek(0, io.SeekCurrent)
	require.NoError(t, err)
	require.Zero(t, position, "local preview commands consumed API stdin")
	require.Zero(t, requests.Load())
	entries, err := os.ReadDir(cache)
	require.NoError(t, err)
	require.Empty(t, entries, "local preview command wrote to the preview cache")
	_, err = os.Stat(filepath.Join(os.Getenv("HOME"), "Library", "Caches", "openai", "image-terminal"))
	require.ErrorIs(t, err, os.ErrNotExist, "local preview command created a macOS gallery")
	_, err = os.Stat(filepath.Join(os.Getenv("HOME"), "Downloads"))
	require.ErrorIs(t, err, os.ErrNotExist, "local preview command created an output folder")
}

func TestMainImagePreviewSetupRejectsArgumentsBeforeNativeWork(t *testing.T) {
	env, preferences := localImageTestEnvironment(t, "http://127.0.0.1:1")
	// Apple Terminal metadata is intentionally incomplete. If validation were
	// skipped, session lookup would fail before any native Terminal access.
	env = append(env, "TERM_PROGRAM=Apple_Terminal", "TERM_SESSION_ID=")
	for _, command := range []string{"setup", "repair", "status"} {
		for _, tc := range []struct {
			name, format, message string
			flags, arguments      []string
		}{
			{"format", "json", "readable output", []string{"--format", "json"}, nil},
			{"transform", "yaml", "cannot use --transform", []string{"--transform", "id"}, nil},
			{"raw", "json", "cannot use --transform", []string{"--raw-output"}, nil},
			{"arguments", "yaml", "takes no arguments", nil, []string{"private\x1b]2;title\a"}},
		} {
			t.Run(command+"/"+tc.name, func(t *testing.T) {
				args := append([]string{"openai", "--format-error", tc.format}, tc.flags...)
				args = append(args, "images", "inline", command)
				args = append(args, tc.arguments...)
				got := runMainDispatchWithEnv(t, "bash", env, args...)
				require.Equal(t, 1, got.code)
				require.Empty(t, got.stdout)
				payload := decodeMainStructuredError(t, tc.format, got.stderr)
				require.Contains(t, payload["message"], tc.message)
				require.NotContains(t, got.stderr, "private")
				require.NotContains(t, got.stderr, "TERM_SESSION_ID")
			})
		}
	}
	_, err := os.Stat(preferences)
	require.ErrorIs(t, err, os.ErrNotExist)
}
