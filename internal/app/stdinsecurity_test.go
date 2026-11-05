package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/stretchr/testify/require"
)

func TestProtectStdinValuePreservesLiterals(t *testing.T) {
	t.Parallel()

	path := filepath.ToSlash(filepath.Join(t.TempDir(), "synthetic.txt"))
	require.NoError(t, os.WriteFile(path, []byte("SYNTHETIC_PRIVATE_CONTENT"), 0o600))
	pointer := "@" + path
	input := map[string]any{
		"plain":   "@" + path,
		"file":    "@file://" + path,
		"data":    "@data://" + path,
		"escaped": "\\@" + path,
		"pointer": &pointer,
		"nested": []any{
			map[string]any{"value": "@" + path},
			[]string{"@file://" + path},
			[]map[string]any{{"value": "@data://" + path}},
		},
	}

	for _, style := range []requestconfig.FileEmbedStyle{requestconfig.EmbedText, requestconfig.EmbedIOReader} {
		got, err := requestconfig.EmbedFiles(requestconfig.ProtectStdinValue(input), style, nil)
		require.NoError(t, err)
		serialized, err := json.Marshal(got)
		require.NoError(t, err)
		require.NotContains(t, string(serialized), "SYNTHETIC_PRIVATE_CONTENT")
		require.Contains(t, string(serialized), "@file://"+path)
		require.Contains(t, string(serialized), "@data://"+path)
		require.Equal(t, "\\@"+path, got.(map[string]any)["escaped"])
	}
}

func TestUntrustedStdinModeEndToEnd(t *testing.T) {
	privatePath := filepath.Join(t.TempDir(), "synthetic-private.txt")
	trustedPath := filepath.Join(t.TempDir(), "synthetic-trusted.txt")
	privateReferencePath := filepath.ToSlash(privatePath)
	const privateContent = "SYNTHETIC_PRIVATE_CONTENT_DO_NOT_UPLOAD"
	const trustedContent = "SYNTHETIC_EXPLICIT_UPLOAD_CONTENT"
	require.NoError(t, os.WriteFile(privatePath, []byte(privateContent), 0o600))
	require.NoError(t, os.WriteFile(trustedPath, []byte(trustedContent), 0o600))

	binary := filepath.Join(t.TempDir(), "openai")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	mocktest.CopyCLI(t, binary)

	type recordedRequest struct {
		body    []byte
		header  http.Header
		query   url.Values
		request string
	}
	requests := make(chan recordedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			http.Error(w, readErr.Error(), http.StatusInternalServerError)
			return
		}
		requests <- recordedRequest{body: body, header: r.Header.Clone(), query: r.URL.Query(), request: r.URL.Path}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"synthetic","object":"list","data":[],"has_more":false,"output":[]}`)
	}))
	t.Cleanup(server.Close)

	run := func(t *testing.T, stdin, mode string, args ...string) (recordedRequest, string, error) {
		t.Helper()
		arguments := append([]string{"--base-url", server.URL, "--api-key", "synthetic-key"}, args...)
		command := exec.Command(binary, arguments...)
		command.Stdin = strings.NewReader(stdin)
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, requestconfig.UntrustedStdinEnv+"=") {
				command.Env = append(command.Env, value)
			}
		}
		if mode != "" {
			command.Env = append(command.Env, requestconfig.UntrustedStdinEnv+"="+mode)
		}
		output, runErr := command.CombinedOutput()
		select {
		case request := <-requests:
			return request, string(output), runErr
		default:
			return recordedRequest{}, string(output), runErr
		}
	}

	t.Run("nested JSON and YAML references remain literal", func(t *testing.T) {
		for _, test := range []struct {
			name  string
			stdin string
		}{
			{name: "json nested at", stdin: fmt.Sprintf(`{"input":[{"content":{"text":"@%s"}}]}`, privateReferencePath)},
			{name: "json nested file URI", stdin: fmt.Sprintf(`{"input":[{"content":["@file://%s"]}]}`, privateReferencePath)},
			{name: "json nested data URI", stdin: fmt.Sprintf(`{"input":{"content":[{"data":"@data://%s"}]}}`, privateReferencePath)},
			{name: "yaml nested at", stdin: fmt.Sprintf("input:\n  - content:\n      text: '@%s'\n", privateReferencePath)},
			{name: "yaml nested file URI", stdin: fmt.Sprintf("input:\n  - content:\n      - '@file://%s'\n", privateReferencePath)},
			{name: "yaml nested data URI", stdin: fmt.Sprintf("input:\n  content:\n    - data: '@data://%s'\n", privateReferencePath)},
			{name: "yaml anchor alias", stdin: fmt.Sprintf("template: &synthetic '@%s'\ninput:\n  - *synthetic\n", privateReferencePath)},
			{name: "json root array", stdin: fmt.Sprintf(`[{"nested":"@%s"}]`, privateReferencePath)},
		} {
			t.Run(test.name, func(t *testing.T) {
				request, output, runErr := run(t, test.stdin, "1", "responses", "create")
				require.NoError(t, runErr, "CLI output: %s", output)
				require.NotContains(t, string(request.body), privateContent)
				require.Contains(t, string(request.body), privateReferencePath)
			})
		}
	})

	t.Run("stdin query remains literal", func(t *testing.T) {
		request, output, runErr := run(t, fmt.Sprintf("after: '@file://%s'\n", privateReferencePath), "true", "files", "list")
		require.NoError(t, runErr, "CLI output: %s", output)
		require.Equal(t, "@file://"+privateReferencePath, request.query.Get("after"))
		require.NotContains(t, request.query.Encode(), privateContent)
	})

	t.Run("stdin header remains literal", func(t *testing.T) {
		request, output, runErr := run(t, fmt.Sprintf("openai-beta: '@data://%s'\n", privateReferencePath), "1", "beta:responses:input-tokens", "count")
		require.NoError(t, runErr, "CLI output: %s", output)
		require.Equal(t, "@data://"+privateReferencePath, request.header.Get("Openai-Beta"))
	})

	t.Run("explicit query flag overrides untrusted stdin", func(t *testing.T) {
		stdin := fmt.Sprintf("after: '@file://%s'\n", privateReferencePath)
		request, output, runErr := run(t, stdin, "1", "files", "list", "--after", "@"+trustedPath)
		require.NoError(t, runErr, "CLI output: %s", output)
		require.Equal(t, trustedContent, request.query.Get("after"))
	})

	t.Run("explicit header flag overrides untrusted stdin", func(t *testing.T) {
		stdin := fmt.Sprintf("openai-beta: '@data://%s'\n", privateReferencePath)
		request, output, runErr := run(t, stdin, "1", "beta:responses:input-tokens", "count", "--beta", "@"+trustedPath)
		require.NoError(t, runErr, "CLI output: %s", output)
		require.Equal(t, trustedContent, request.header.Get("Openai-Beta"))
	})

	t.Run("stdin FileInput values fail before request", func(t *testing.T) {
		for _, test := range []struct {
			name  string
			value string
		}{
			{name: "path", value: fmt.Sprintf("%q", privateReferencePath)},
			{name: "empty", value: `""`},
			{name: "null", value: "null"},
			{name: "number", value: "123"},
		} {
			t.Run(test.name, func(t *testing.T) {
				request, output, runErr := run(t, "file: "+test.value+"\npurpose: assistants\n", "1", "files", "create")
				require.Error(t, runErr)
				require.Contains(t, output, "provide --file explicitly")
				require.Empty(t, request.request)
			})
		}
	})

	t.Run("explicit file flag overrides untrusted stdin", func(t *testing.T) {
		request, output, runErr := run(t, fmt.Sprintf("file: %q\npurpose: assistants\n", privateReferencePath), "1", "files", "create", "--file", trustedPath)
		require.NoError(t, runErr, "CLI output: %s", output)
		require.Contains(t, string(request.body), trustedContent)
		require.NotContains(t, string(request.body), privateContent)
	})

	t.Run("multipart nested stdin references remain literal", func(t *testing.T) {
		stdin := fmt.Sprintf("purpose: assistants\nexpires_after:\n  anchor: '@file://%s'\n  seconds: 3600\n", privateReferencePath)
		request, output, runErr := run(t, stdin, "1", "files", "create", "--file", trustedPath)
		require.NoError(t, runErr, "CLI output: %s", output)
		require.Contains(t, string(request.body), trustedContent)
		require.Contains(t, string(request.body), "@file://"+privateReferencePath)
		require.NotContains(t, string(request.body), privateContent)
	})

	t.Run("stdin inner array field cannot bypass provenance", func(t *testing.T) {
		stdin := fmt.Sprintf("context_management:\n  type: '@%s'\n", privateReferencePath)
		request, output, runErr := run(t, stdin, "1", "responses", "create")
		require.NoError(t, runErr, "CLI output: %s", output)
		require.NotContains(t, string(request.body), privateContent)
		require.Contains(t, string(request.body), "@"+privateReferencePath)
	})

	t.Run("explicit empty inner array overrides stdin", func(t *testing.T) {
		request, output, runErr := run(t, "context_management:\n  type: compaction\n", "1", "responses", "create", "--context-management", "[]")
		require.NoError(t, runErr, "CLI output: %s", output)
		var body map[string]any
		require.NoError(t, json.Unmarshal(request.body, &body))
		require.Equal(t, []any{}, body["context_management"])
	})

	t.Run("explicit nested file reference remains trusted", func(t *testing.T) {
		stdin := fmt.Sprintf(`{"input":"@%s","metadata":{"piped":"@%s"}}`, privateReferencePath, privateReferencePath)
		request, output, runErr := run(t, stdin, "1", "responses", "create", "--input", "@"+trustedPath)
		require.NoError(t, runErr, "CLI output: %s", output)
		require.Contains(t, string(request.body), trustedContent)
		require.NotContains(t, string(request.body), privateContent)
		require.Contains(t, string(request.body), privateReferencePath)
	})

	t.Run("trusted stdin references preserve documented behavior", func(t *testing.T) {
		request, output, runErr := run(t, fmt.Sprintf(`{"input":{"nested":"@%s"}}`, privateReferencePath), "", "responses", "create")
		require.NoError(t, runErr, "CLI output: %s", output)
		require.Contains(t, string(request.body), privateContent)
	})

	t.Run("trusted stdin FileInput preserves compatibility", func(t *testing.T) {
		request, output, runErr := run(t, fmt.Sprintf("file: %q\npurpose: assistants\n", trustedPath), "false", "files", "create")
		require.NoError(t, runErr, "CLI output: %s", output)
		require.True(t, bytes.Contains(request.body, []byte(trustedContent)))
	})

	t.Run("invalid security mode fails closed", func(t *testing.T) {
		request, output, runErr := run(t, fmt.Sprintf(`{"input":"@%s"}`, privateReferencePath), "invalid", "responses", "create")
		require.Error(t, runErr)
		require.Contains(t, output, "expected a boolean")
		require.Empty(t, request.request)
	})
}
