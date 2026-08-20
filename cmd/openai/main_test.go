package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAPIErrorOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid model","type":"invalid_request_error"}}`))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	binaryName := "openai"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binary := filepath.Join(t.TempDir(), binaryName)
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	buildOutput, err := build.CombinedOutput()
	require.NoError(t, err, "output: %s", buildOutput)

	command := exec.CommandContext(
		ctx,
		binary,
		"--base-url", server.URL,
		"--api-key", "key",
		"--format-error", "raw",
		"models", "retrieve",
		"--model", "missing",
	)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	err = command.Run()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	require.Equal(t, 1, exitErr.ExitCode())
	require.Empty(t, stdout.String())
	require.Contains(t, stderr.String(), "400 Bad Request")
	require.Contains(t, stderr.String(), `"message":"invalid model"`)
}

func TestErrorOutputFormat(t *testing.T) {
	require.Equal(t, "json", errorOutputFormat("explore"))
	require.Equal(t, "json", errorOutputFormat("EXPLORE"))
	require.Equal(t, "raw", errorOutputFormat("raw"))
}
