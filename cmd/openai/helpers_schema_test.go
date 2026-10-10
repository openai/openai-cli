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

	"github.com/openai/openai-cli/pkg/custom"
	"github.com/stretchr/testify/require"
)

// Match production early dispatch when the process harness launches compilation.
func init() {
	if handled, code := custom.RunSchemaValidationHelper(os.Args, os.Stdin, os.Stdout); handled {
		os.Exit(code)
	}
}

const helperSchemaFixture = `{"type":"object","properties":{"invoice_id":{"type":"string"}},"required":["invoice_id"],"additionalProperties":false}`

func helperSchemaResponse(schema string) string {
	encoded, _ := json.Marshal(schema)
	return `{"id":"resp_fixture","object":"response","status":"completed","model":"gpt-4.1-mini-2025-04-14","output":[{"id":"msg_fixture","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":` + string(encoded) + `,"annotations":[]}]}]}`
}

func helperSchemaArgs(path string) []string {
	return []string{"helpers", "schema", "--description", "An invoice", "--model", "gpt-4.1-mini-2025-04-14", "--output", path}
}

func TestMainSchemaHelperRequestAndFormats(t *testing.T) {
	for _, format := range []string{"text", "json", "jsonl", "yaml", "raw", "pretty"} {
		t.Run(format, func(t *testing.T) {
			var count atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				require.Equal(t, "POST", r.Method)
				require.Equal(t, "/custom/responses", r.URL.Path)
				require.Equal(t, "Bearer sk-fake-explicit", r.Header.Get("Authorization"))
				require.Equal(t, "org-fixture", r.Header.Get("OpenAI-Organization"))
				require.Equal(t, "proj-fixture", r.Header.Get("OpenAI-Project"))
				require.Equal(t, "literal:@fixture", r.Header.Get("X-Fixture"))
				var body map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				require.Equal(t, "@literal description", body["input"])
				require.Equal(t, false, body["store"])
				require.Equal(t, float64(1234), body["max_output_tokens"])
				require.Equal(t, "gpt-4.1-mini-2025-04-14", body["model"])
				require.Equal(t, map[string]any{"format": map[string]any{"type": "json_object"}}, body["text"])
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, helperSchemaResponse(helperSchemaFixture))
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "invoice.json")
			args := []string{"openai", "--base-url", server.URL + "/custom/", "--api-key", "sk-fake-explicit", "--format", format,
				"helpers", "schema", "--description", "@literal description", "--model", "gpt-4.1-mini-2025-04-14", "--output", path,
				"--max-output-tokens", "1234", "--project", "proj-fixture", "--organization", "org-fixture", "--header", "X-Fixture: literal:@fixture"}
			got := runMainDispatchWithEnv(t, "", []string{"OPENAI_API_KEY=sk-fake-env", "OPENAI_PROJECT_ID=proj-env"}, args...)
			bytes, err := os.ReadFile(path)
			require.NoError(t, err, "%+v", got)
			require.Zero(t, got.code, "%+v", got)
			require.Equal(t, helperSchemaFixture, string(bytes))
			require.EqualValues(t, 1, count.Load())
			require.Contains(t, got.stdout, "not checked")
			if format == "json" || format == "jsonl" || format == "raw" {
				require.True(t, json.Valid([]byte(got.stdout)), got.stdout)
				require.Empty(t, got.stderr)
			}
		})
	}
}

func TestMainSchemaHelperFailures(t *testing.T) {
	for _, tc := range []struct {
		name, response, message string
		status                  int
	}{
		{"refusal", `{"status":"completed","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"refusal","refusal":"private text"}]}]}`, "refused", 200},
		{"incomplete", `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}`, "did not complete", 200},
		{"empty", `{"status":"completed","output":[]}`, "no schema", 200},
		{"invalid schema", helperSchemaResponse(`{"type":"wrong"}`), "compilation", 200},
		{"external ref", helperSchemaResponse(`{"$ref":"file:///private/secret"}`), "compilation", 200},
		{"malformed schema", helperSchemaResponse(`{"type":`), "compilation", 200},
		{"HTTP error", `{"error":{"message":"fixture","type":"server_error"}}`, "fixture", 500},
		{"empty HTTP error", `{}`, "", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var count atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.response)
			}))
			defer server.Close()
			dir := t.TempDir()
			path := filepath.Join(dir, "invoice.json")
			args := append([]string{"--format", "json"}, helperSchemaArgs(path)...)
			got := runReadableCommand(t, server, args...)
			require.Equal(t, 1, got.code, "%+v", got)
			require.Empty(t, got.stdout)
			require.True(t, json.Valid([]byte(got.stderr)), got.stderr)
			require.Contains(t, got.stderr, tc.message)
			require.NotContains(t, got.stderr, "private text")
			require.NotContains(t, got.stderr, "/private/secret")
			require.EqualValues(t, 1, count.Load())
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestMainSchemaHelperPreflightAndRecovery(t *testing.T) {
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, helperSchemaResponse(helperSchemaFixture))
	}))
	defer server.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.json")
	require.NoError(t, os.WriteFile(path, []byte("original"), 0600))
	got := runReadableCommand(t, server, helperSchemaArgs(path)...)
	require.Equal(t, 1, got.code)
	require.Contains(t, got.stderr, "Choose a new --output")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "original", string(data))
	require.Zero(t, count.Load())
	// Follow the actual recovery instruction against the same controlled service.
	newPath := filepath.Join(dir, "new.json")
	got = runReadableCommand(t, server, append([]string{"--quiet", "--transform", "saved", "--raw-output"}, helperSchemaArgs(newPath)...)...)
	require.Zero(t, got.code, "%+v", got)
	require.Empty(t, got.stderr)
	require.Equal(t, newPath+"\n", got.stdout)
	require.EqualValues(t, 1, count.Load())
	stdin, err := os.CreateTemp(t.TempDir(), "body")
	require.NoError(t, err)
	defer stdin.Close()
	_, err = stdin.WriteString(`{"model":"unwanted"}`)
	require.NoError(t, err)
	_, err = stdin.Seek(0, 0)
	require.NoError(t, err)
	args := append([]string{"openai", "--base-url", server.URL, "--api-key", "sk-fake-fixture", "--format", "json"}, helperSchemaArgs(filepath.Join(dir, "stdin.json"))...)
	got = runMainDispatchWithStdin(t, "", nil, stdin, args...)
	require.Equal(t, 1, got.code)
	require.Contains(t, got.stderr, "does not accept stdin")
	require.EqualValues(t, 1, count.Load())
}

func TestMainSchemaHelperCancellation(t *testing.T) {
	ready := make(chan struct{})
	release := make(chan struct{})
	disconnected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// net/http starts disconnect monitoring after the POST body reaches EOF.
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
			return
		}
		close(ready)
		select {
		case <-r.Context().Done():
			close(disconnected)
		case <-release:
		}
	}))
	defer func() {
		close(release)
		server.Close()
	}()
	dir := t.TempDir()
	path := filepath.Join(dir, "canceled.json")
	executable, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	args := append([]string{"-test.run=^TestMainDispatchProcess$", "--", "openai", "--base-url", server.URL, "--api-key", "sk-fake-fixture"}, helperSchemaArgs(path)...)
	child := exec.CommandContext(ctx, executable, args...)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(value), "OPENAI_") {
			child.Env = append(child.Env, value)
		}
	}
	child.Env = append(child.Env, "OPENAI_CLI_MAIN_DISPATCH_PROCESS=1")
	var stdout, stderr bytes.Buffer
	child.Stdout = &stdout
	child.Stderr = &stderr
	require.NoError(t, child.Start())
	waited := false
	defer func() {
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("request did not start")
	}
	if err := child.Process.Signal(os.Interrupt); err != nil {
		_ = child.Process.Kill()
		_ = child.Wait()
		waited = true
		t.Skipf("interrupt unavailable: %v", err)
	}
	err = child.Wait()
	waited = true
	require.Error(t, err)
	require.Equal(t, 130, child.ProcessState.ExitCode(), stderr.String())
	require.Empty(t, stdout.String())
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
	select {
	case <-disconnected:
	case <-time.After(3 * time.Second):
		t.Fatal("synthetic service did not observe request cancellation")
	}
}

func TestMainSchemaHelperHelp(t *testing.T) {
	for _, args := range [][]string{{"openai", "helpers", "schema", "--help"}, {"openai", "help", "helpers", "schema"}, {"openai", "--project", "proj-fixture", "helpers", "schema", "-h"}} {
		got := runMainDispatch(t, "", args...)
		require.Zero(t, got.code, "%+v", got)
		for _, word := range []string{"--model", "--description", "--output", "paid", "Structured Outputs", "--max-output-tokens"} {
			require.Contains(t, got.stdout, word)
		}
		require.Empty(t, got.stderr)
	}
}
