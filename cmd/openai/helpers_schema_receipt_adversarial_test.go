package main

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestMainSchemaHelperAdversarialReceiptFailureKeepsSavedArtifact(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, helperSchemaResponse(helperSchemaFixture))
			}))
			defer server.Close()
			directory := t.TempDir()
			path := filepath.Join(directory, "invoice.json")
			executable, err := os.Executable()
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			args := []string{"-test.run=^TestMainDispatchProcess$", "--", "openai", "--base-url", server.URL, "--api-key", "sk-fake-fixture", "--format", format}
			args = append(args, helperSchemaArgs(path)...)
			child := exec.CommandContext(ctx, executable, args...)
			child.WaitDelay = time.Second
			for _, value := range os.Environ() {
				if !strings.HasPrefix(strings.ToUpper(value), "OPENAI_") {
					child.Env = append(child.Env, value)
				}
			}
			child.Env = append(child.Env, "OPENAI_CLI_MAIN_DISPATCH_PROCESS=1")
			// A read-only descriptor fails writes even for privileged test users.
			// This exercises the real stdout sink without relying on permissions.
			sinkPath := filepath.Join(t.TempDir(), "read-only-stdout")
			const prior = "unchanged synthetic stdout destination"
			require.NoError(t, os.WriteFile(sinkPath, []byte(prior), 0600))
			sink, err := os.Open(sinkPath)
			require.NoError(t, err)
			defer sink.Close()
			child.Stdout = sink
			var stderr bytes.Buffer
			child.Stderr = &stderr
			err = child.Run()
			require.Error(t, err)
			require.NoError(t, ctx.Err(), "command must finish before its outer deadline")
			require.NotNil(t, child.ProcessState)
			require.Equal(t, 1, child.ProcessState.ExitCode(), stderr.String())
			require.Contains(t, stderr.String(), "Schema saved, but its receipt could not be displayed")
			require.NotContains(t, stderr.String(), "canceled")
			if format == "json" {
				require.True(t, json.Valid(stderr.Bytes()), stderr.String())
			}
			require.EqualValues(t, 1, requests.Load())
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, helperSchemaFixture, string(data))
			entries, err := os.ReadDir(directory)
			require.NoError(t, err)
			require.Len(t, entries, 1, "staging cleanup must precede receipt output")
			require.Equal(t, "invoice.json", entries[0].Name())
			data, err = os.ReadFile(sinkPath)
			require.NoError(t, err)
			require.Equal(t, prior, string(data))
		})
	}
}
