package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMainRequestFileWaitsForPipedRequest(t *testing.T) {
	const response = `{"id":"resp_synthetic","object":"response","output":[]}`
	server, requests := globalFlagsServer(t, response)
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { reader.Close() })
	t.Cleanup(func() { writer.Close() })

	// The flags already make a valid request. A readiness probe would dispatch
	// that request while the producer still prepares its store preference.
	written := make(chan error, 1)
	time.AfterFunc(350*time.Millisecond, func() {
		_, err := io.WriteString(writer, `{"model":"from-document","store":false}`)
		writer.Close()
		written <- err
	})
	got := runShellFileCommand(t, server, reader, nil, "responses", "create",
		"--request-file", "-", "--model", "from-flag", "--input", "synthetic")
	require.NoError(t, <-written)
	require.Equal(t, 0, got.code, got.stderr)
	require.Empty(t, got.stderr)
	require.JSONEq(t, response, got.stdout)
	req := globalFlagsOneRequest(t, requests)
	require.Equal(t, http.MethodPost, req.method)
	require.Equal(t, "/responses", req.path)
	require.JSONEq(t, `{"model":"from-flag","input":"synthetic","store":false}`, string(req.body))
}

func TestMainRequestFileNamedYAMLLeavesStdinUnread(t *testing.T) {
	const response = `{"id":"chatcmpl_synthetic","object":"chat.completion","choices":[]}`
	server, requests := globalFlagsServer(t, response)
	path := filepath.Join(t.TempDir(), "whole request.yaml")
	document := "model: from-file\nmessages:\n  - role: user\n    content: synthetic\n"
	require.NoError(t, os.WriteFile(path, []byte(document), 0600))
	const unrelated = `{"store":true}`
	stdin := shellFileInput(t, []byte(unrelated))

	got := runShellFileCommand(t, server, stdin, nil, "chat", "completions", "create",
		"--request-file", path, "--model", "from-flag")
	require.Equal(t, 0, got.code, got.stderr)
	require.Empty(t, got.stderr)
	require.JSONEq(t, response, got.stdout)
	req := globalFlagsOneRequest(t, requests)
	require.Equal(t, http.MethodPost, req.method)
	require.Equal(t, "/chat/completions", req.path)
	require.JSONEq(t, `{"model":"from-flag","messages":[{"role":"user","content":"synthetic"}]}`, string(req.body))
	left, err := io.ReadAll(stdin)
	require.NoError(t, err)
	require.Equal(t, unrelated, string(left))
}

func TestMainRequestFileNamedDocumentWithExplicitStdinField(t *testing.T) {
	server, requests := globalFlagsServer(t, `{"id":"resp_synthetic","object":"response","output":[]}`)
	path := filepath.Join(t.TempDir(), "request.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"model":"synthetic","store":false}`), 0600))
	stdin := shellFileInput(t, []byte("synthetic input\n"))
	got := runShellFileCommand(t, server, stdin, nil, "responses", "create", "--request-file", path, "--input", "@-")
	require.Equal(t, 0, got.code, got.stderr)
	require.JSONEq(t, `{"model":"synthetic","store":false,"input":"synthetic input\n"}`, string(globalFlagsOneRequest(t, requests).body))
}

func TestMainRequestFileAndStdinCanReadSameRegularFile(t *testing.T) {
	server, requests := globalFlagsServer(t, `{}`)
	const source = "model: synthetic\n"
	stdin := shellFileInput(t, []byte(source))
	got := runShellFileCommand(t, server, stdin, nil, "responses", "create",
		"--request-file", stdin.Name(), "--input", "@-")
	require.Equal(t, 0, got.code, got.stderr)
	require.JSONEq(t, `{"model":"synthetic","input":"model: synthetic\n"}`, string(globalFlagsOneRequest(t, requests).body))
}

func TestMainRequestFileEmbeddedReferenceCannotClaimStdin(t *testing.T) {
	const document = `{"model":"synthetic","input":"@-"}`
	path := filepath.Join(t.TempDir(), "request.json")
	require.NoError(t, os.WriteFile(path, []byte(document), 0600))
	for _, untrusted := range []string{"true", "false"} {
		t.Run("untrusted="+untrusted, func(t *testing.T) {
			server, requests := globalFlagsServer(t, `{"id":"resp_synthetic","object":"response","output":[]}`)
			const unrelated = "synthetic unrelated input"
			stdin := shellFileInput(t, []byte(unrelated))
			got := runShellFileCommand(t, server, stdin, []string{"OPENAI_UNTRUSTED_STDIN=" + untrusted},
				"responses", "create", "--request-file", path)
			if untrusted == "true" {
				require.Equal(t, 0, got.code, got.stderr)
				require.JSONEq(t, document, string(globalFlagsOneRequest(t, requests).body))
			} else {
				require.NotZero(t, got.code)
				require.Empty(t, got.stdout)
				require.Empty(t, requests)
			}
			left, err := io.ReadAll(stdin)
			require.NoError(t, err)
			require.Equal(t, unrelated, string(left))
		})
	}
}

func TestMainRequestFileUntrustedStdinPreservesLiteralFileReference(t *testing.T) {
	server, requests := globalFlagsServer(t, `{"id":"resp_synthetic","object":"response","output":[]}`)
	path := filepath.Join(t.TempDir(), "local-input")
	require.NoError(t, os.WriteFile(path, []byte("synthetic local file content"), 0600))
	document, err := json.Marshal(map[string]string{"model": "synthetic", "input": "@" + path})
	require.NoError(t, err)
	got := runShellFileCommand(t, server, shellFileInput(t, document), []string{"OPENAI_UNTRUSTED_STDIN=true"},
		"responses", "create", "--request-file=-")
	require.Equal(t, 0, got.code, got.stderr)
	require.JSONEq(t, string(document), string(globalFlagsOneRequest(t, requests).body))
}

func TestMainRequestFileRejectsInvalidInputsBeforeDispatch(t *testing.T) {
	for _, tc := range []struct {
		name        string
		args        []string
		input       string
		leavesStdin bool
	}{
		{"two stdin targets", []string{"responses", "create", "--model", "synthetic", "--input", "@-", "--request-file", "-"}, `{"synthetic-marker":true}`, true},
		{"stdin alias conflict", []string{"responses", "create", "--input", "@-", "--request-file", "/dev/stdin"}, `{"synthetic-marker":true}`, true},
		{"duplicate source", []string{"responses", "create", "--request-file", "-", "--request-file", "-"}, `{"model":"synthetic","input":"synthetic-marker"}`, true},
		{"invalid document", []string{"responses", "create", "--model", "synthetic", "--input", "synthetic", "--request-file", "-"}, "synthetic-marker: [}", false},
		{"empty document", []string{"responses", "create", "--model", "synthetic", "--input", "synthetic", "--request-file", "-"}, "", false},
		{"scalar document", []string{"responses", "create", "--model", "synthetic", "--input", "synthetic", "--request-file", "-"}, "synthetic-marker", false},
		{"multiple documents", []string{"responses", "create", "--model", "synthetic", "--input", "synthetic", "--request-file", "-"}, "store: true\n---\nstore: false\n", false},
		{"unsupported command", []string{"models", "list", "--request-file", "-"}, `{"synthetic-marker":true}`, true},
		{"unsupported sibling", []string{"responses", "delete", "resp_synthetic", "--request-file", "-"}, `{"synthetic-marker":true}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, requests := globalFlagsServer(t, `{}`)
			stdin := shellFileInput(t, []byte(tc.input))
			got := runShellFileCommand(t, server, stdin, nil, tc.args...)
			require.NotZero(t, got.code)
			require.Empty(t, got.stdout)
			var diagnostic struct{ Message string }
			require.NoError(t, json.Unmarshal([]byte(got.stderr), &diagnostic))
			require.NotEmpty(t, diagnostic.Message)
			require.NotContains(t, got.stderr, "synthetic-marker")
			require.Empty(t, requests)
			if tc.leavesStdin {
				left, err := io.ReadAll(stdin)
				require.NoError(t, err)
				require.Equal(t, tc.input, string(left))
			}
		})
	}
}

func TestMainRequestFileHelp(t *testing.T) {
	for _, args := range [][]string{{"responses", "create"}, {"chat", "completions", "create"}} {
		server, requests := globalFlagsServer(t, `{}`)
		stdin := shellFileInput(t, []byte("for the next command"))
		got := runShellFileCommand(t, server, stdin, nil, append(args, "--request-file", "-", "--help")...)
		require.Equal(t, 0, got.code, got.stderr)
		require.Empty(t, got.stderr)
		require.Contains(t, got.stdout, "--request-file")
		require.Empty(t, requests)
		left, err := io.ReadAll(stdin)
		require.NoError(t, err)
		require.Equal(t, "for the next command", string(left))
	}
}
