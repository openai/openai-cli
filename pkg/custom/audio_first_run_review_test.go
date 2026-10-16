package custom

import (
	"context"
	"os"
	"testing"

	"github.com/charmbracelet/x/term"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestAudioReviewLocalCommandsSuppressFirstRunSetup(t *testing.T) {
	root := audioFirstRunTestRoot()
	getenv := func(name string) string {
		if name == "TERM" {
			return "xterm-256color"
		}
		return ""
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		for _, leaf := range []string{"history", "inspect", "play", "export", "licenses"} {
			for _, args := range [][]string{
				{"openai", "audio", leaf},
				{"openai", "--project", "synthetic-project", "audio", leaf},
				{"openai", "audio", "--project", "synthetic-project", leaf},
				{"openai", "audio", leaf, "--project", "synthetic-project"},
				{"openai", "--format", "json", "audio", leaf},
				{"openai", "audio", "--format", "json", leaf},
				{"openai", "help", "audio", leaf},
			} {
				require.True(t, imagePickerFirstRunEligible(args, getenv, true, true, true, shell), "nil-root policy remains unchanged")
				require.True(t, skipAudioFirstRunSetup(root, args), "registered local audio path must skip setup: %q", args)
			}
		}
	}
	for _, args := range [][]string{
		{"openai", "models", "list"}, {"openai", "audio", "speak", "--input", "history"},
		{"openai", "audio", "transcribe", "--file", "inspect"}, {"openai", "audio", "translate", "--file", "play"},
		{"openai", "--project", "audio", "models", "list"},
	} {
		require.False(t, skipAudioFirstRunSetup(root, args), "%q", args)
	}
}

func TestAudioFirstRunProductionGuardTraversesBeforeBlockedWorkerPTY(t *testing.T) {
	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) || !term.IsTerminal(os.Stderr.Fd()) {
		t.Skip("requires a reserved native PTY with stdin, stdout, and stderr attached")
	}
	// A filled admission gate prevents any setup worker and therefore all profile
	// and filesystem setup. Traversal must still happen synchronously before it.
	select {
	case imagePickerFirstRunWorker <- struct{}{}:
	default:
		t.Fatal("first-run worker gate was already occupied")
	}
	defer func() { <-imagePickerFirstRunWorker }()
	root := audioFirstRunTestRoot()
	spy := &audioFirstRunFlagSpy{StringFlag: &cli.StringFlag{Name: "guard-spy"}}
	root.Flags = append(root.Flags, spy)
	SetupImagePickerShellOnFirstRun(context.Background(), []string{"openai", "--guard-spy", "audio", "audio", "history"}, root)
	require.Positive(t, spy.names.Load(), "declarations were not traversed before the blocked worker gate")
	require.Positive(t, spy.values.Load(), "the synchronous guard did not inspect declared flag arity")
	require.Len(t, imagePickerFirstRunWorker, 1, "the occupied worker gate changed")
	require.False(t, spy.IsSet(), "startup guard mutated a registered flag")
	namesBefore := spy.names.Load()
	SetupImagePickerShellOnFirstRun(context.Background(), []string{"openai", "--guard-spy", "audio", "audio", "licenses"}, root)
	require.Greater(t, spy.names.Load(), namesBefore, "new local metadata leaf bypassed the declaration traversal")
	require.Len(t, imagePickerFirstRunWorker, 1)
	// Even with no local classification, a nil root must retain legacy behavior.
	SetupImagePickerShellOnFirstRun(context.Background(), []string{"openai", "models", "list"}, nil)
	require.Len(t, imagePickerFirstRunWorker, 1)
}
