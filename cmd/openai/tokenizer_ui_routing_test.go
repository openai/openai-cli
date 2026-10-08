package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMainTokenizerEditorNeverCapturesRedirectedInput(t *testing.T) {
	server, requests := localUtilitiesRequestTrap(t)
	env := append(localUtilitiesEnvironment(t), "OPENAI_BASE_URL="+server.URL, "TERM=xterm-256color", "CI=0")
	for _, flags := range [][]string{nil, {"--format", "text"}} {
		t.Run(strings.Join(flags, "/"), func(t *testing.T) {
			input, producer, err := os.Pipe()
			require.NoError(t, err)
			defer input.Close()
			defer producer.Close()
			// Keep the producer open without writing. Reading for EOF would block.
			args := append([]string{"openai", "tokenizer"}, flags...)
			got := runMainDispatchWithStdin(t, "bash", env, input, args...)
			require.Zero(t, got.code, "%s", got.stderr)
			require.Empty(t, got.stderr)
			require.Contains(t, got.stdout, "tokenizer")
			require.Contains(t, got.stdout, "count")
			require.Contains(t, got.stdout, "inspect")
			require.NotContains(t, got.stdout, "\x1b")
		})
	}
	require.Zero(t, requests.Load())
}

func TestMainTokenizerEditorPlainFallbackEnvironments(t *testing.T) {
	env := append(localUtilitiesEnvironment(t), "OPENAI_BASE_URL=://synthetic-private-endpoint",
		"OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic-private/missing-cert")
	for _, environment := range [][]string{
		{"TERM=dumb"}, {"TERM=Dumb"}, {"TERM=xterm-256color", "CI=true"},
		{"TERM=xterm-256color", "GITHUB_ACTIONS=1"}, {"TERM=xterm-256color", "NO_COLOR=1"},
		{"TERM=xterm-256color", "FORCE_COLOR=1"},
	} {
		t.Run(strings.Join(environment, "/"), func(t *testing.T) {
			got := runMainDispatchWithEnv(t, "bash", append(env, environment...), "openai", "tokenizer")
			require.Zero(t, got.code, "%s", got.stderr)
			require.Empty(t, got.stderr)
			require.Contains(t, got.stdout, "count")
			require.Contains(t, got.stdout, "inspect")
			require.NotContains(t, got.stdout, "\x1b")
			require.NotContains(t, got.stdout+got.stderr, "synthetic-private-")
		})
	}
}

func TestMainTokenizerEditorRejectsUnsupportedModes(t *testing.T) {
	env := append(localUtilitiesEnvironment(t), "OPENAI_BASE_URL=://synthetic-private-endpoint")
	for _, flags := range [][]string{
		{"--format", "json"}, {"--format", "jsonl"}, {"--transform", ".synthetic-private-field"},
		{"--raw-output"}, {"--text", "synthetic-private-input"}, {"synthetic-private-argument"},
	} {
		t.Run(strings.Join(flags, "/"), func(t *testing.T) {
			args := append([]string{"openai", "--format-error", "json", "tokenizer"}, flags...)
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			require.NotZero(t, got.code)
			require.Empty(t, got.stdout)
			_ = decodeMainStructuredError(t, "json", got.stderr)
			require.NotContains(t, got.stderr, "synthetic-private-")
			require.NotContains(t, got.stderr, "\x1b")
		})
	}
}

func TestMainTokenizerEditorHelpAndDiscoveryStayNonInteractive(t *testing.T) {
	env := append(localUtilitiesEnvironment(t), "OPENAI_BASE_URL=://synthetic-private-endpoint")
	for _, args := range [][]string{
		{"openai", "tokenizer", "-h"}, {"openai", "tokenizer", "--help"},
		{"openai", "help", "tokenizer"}, {"openai", "help", "--all", "tokenizer"},
	} {
		t.Run(strings.Join(args[1:], "/"), func(t *testing.T) {
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			require.Zero(t, got.code, "%s", got.stderr)
			require.Empty(t, got.stderr)
			require.Contains(t, got.stdout, "count")
			require.Contains(t, got.stdout, "inspect")
			require.NotContains(t, got.stdout, "Key setup:")
			require.NotContains(t, got.stdout, "__preview")
			require.NotContains(t, got.stdout, "__output")
			require.NotContains(t, got.stdout, "\x1b")
		})
	}
	rootHelp := runMainDispatchWithEnv(t, "bash", env, "openai", "--help")
	require.Zero(t, rootHelp.code, "%s", rootHelp.stderr)
	require.Contains(t, rootHelp.stdout, "LOCAL TOOLS")
	require.Contains(t, rootHelp.stdout, "tokenizer")
	require.Contains(t, rootHelp.stdout, "codex")
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		got := runMainDispatchWithEnv(t, style, env, mainCompletionArgs(style, "tokenizer", "ins")...)
		require.Zero(t, got.code, "%s", got.stderr)
		require.Empty(t, got.stderr)
		require.Contains(t, got.stdout, "inspect")
		require.NotContains(t, got.stdout, "\x1b")
		hidden := runMainDispatchWithEnv(t, style, env, mainCompletionArgs(style, "tokenizer", "__")...)
		require.Zero(t, hidden.code, "%s", hidden.stderr)
		require.Empty(t, hidden.stdout)
		require.Empty(t, hidden.stderr)
	}
	hiddenHelp := runMainDispatchWithEnv(t, "bash", env, "openai", "help", "tokenizer", "__preview")
	require.NotZero(t, hiddenHelp.code)
	require.Empty(t, hiddenHelp.stdout)
	hiddenHelp = runMainDispatchWithEnv(t, "bash", env, "openai", "help", "tokenizer", "__output")
	require.NotZero(t, hiddenHelp.code)
	require.Empty(t, hiddenHelp.stdout)
}

func TestMainTokenizerEditorLeavesScriptResultsUnchanged(t *testing.T) {
	env := append(localUtilitiesEnvironment(t), "TERM=xterm-256color", "FORCE_COLOR=1")
	for _, argv := range [][]string{
		{"openai", "--format", "json", "tokenizer", "count", "--text", "Hello, world!"},
		{"openai", "tokenizer", "--format", "json", "count", "--text", "Hello, world!"},
		{"openai", "tokenizer", "count", "--text", "Hello, world!", "--format", "json"},
	} {
		got := runMainDispatchWithEnv(t, "bash", env, argv...)
		require.Zero(t, got.code, "%s", got.stderr)
		require.Empty(t, got.stderr)
		require.JSONEq(t, `{"encoding":"o200k_base","input_bytes":13,"token_count":4}`, got.stdout)
		require.NotContains(t, got.stdout, "\x1b")
	}
	got := runMainDispatchWithEnv(t, "bash", env, "openai", "tokenizer", "inspect", "--text", "", "--format", "json")
	require.Zero(t, got.code, "%s", got.stderr)
	require.Empty(t, got.stderr)
	require.JSONEq(t, `{"encoding":"o200k_base","input_bytes":0,"token_count":0,"tokens":[]}`, got.stdout)
}
