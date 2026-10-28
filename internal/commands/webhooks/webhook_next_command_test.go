package webhooks

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

func TestWebhookCommandShellCrossesOnlyRecognizedGoLauncher(t *testing.T) {
	t.Setenv("SHELL", "/bin/bash")
	for _, tc := range []struct {
		name, parent, caller, want string
		goRun                      bool
		lookups                    int
	}{
		{"ordinary Bash", "/bin/bash", "", "bash", false, 0},
		{"ordinary zsh", "-zsh", "", "zsh", false, 0},
		{"ordinary fish", "/usr/bin/fish", "", "fish", false, 0},
		{"ordinary PowerShell", `C:\PowerShell\pwsh.exe`, "", "pwsh", false, 0},
		{"recognized invocation with immediate shell", "bash", "", "bash", true, 0},
		{"Go without invocation evidence", "/usr/local/go/bin/go", "bash", "", false, 0},
		{"Go from Bash", "/usr/local/go/bin/go", "bash", "bash", true, 1},
		{"Go from zsh", "go", "zsh", "zsh", true, 1},
		{"Go from fish", "go", "fish", "fish", true, 1},
		{"Windows Go from PowerShell", `C:\Go\bin\GO.EXE`, `C:\PowerShell\pwsh.exe`, "pwsh", true, 1},
		{"second Go wrapper stops traversal", "go", "go", "", true, 1},
		{"unsupported Go caller", "go", "python3", "", true, 1},
		{"unsupported direct caller", "python3", "bash", "", true, 0},
		{"different Go tool", "gofmt", "bash", "", true, 0},
		{"unknown shell", "/bin/sh", "bash", "", true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lookups := 0
			got := webhookCommandShellName(t.Context(), tc.goRun, tc.parent, func() (string, error) {
				lookups++
				return tc.caller, nil
			})
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.lookups, lookups)
		})
	}
}

func TestWebhookCommandShellLookupFailureAndCancellation(t *testing.T) {
	t.Setenv("SHELL", "/bin/bash")
	t.Run("lookup failure does not guess", func(t *testing.T) {
		got := webhookCommandShellName(t.Context(), true, "go", func() (string, error) {
			return "bash", errors.New("synthetic unavailable parent")
		})
		require.Empty(t, got)
	})
	t.Run("already canceled performs no lookup", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		got := webhookCommandShellName(ctx, true, "go", func() (string, error) {
			t.Fatal("canceled receipt inspected another process")
			return "bash", nil
		})
		require.Empty(t, got)
		require.Empty(t, webhookCommandShell(ctx, webhookInvocation{goRun: true}))
	})
	t.Run("canceled lookup discards result", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		got := webhookCommandShellName(ctx, true, "go", func() (string, error) {
			cancel()
			return "bash", nil
		})
		require.Empty(t, got)
	})
	t.Run("unsafe request context omits lookup", func(t *testing.T) {
		require.Empty(t, webhookCommandShell(t.Context(), webhookInvocation{goRun: true, omitHint: true}))
	})
}

func TestWebhookCommandGoRunInvocationCapturesCheckout(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "checkout's folder")
	invocation := webhookInvocation{
		display: "go run ./cmd/openai", executable: "/tmp/go-build123/b001/exe/openai",
		goRun: true, goRunDir: directory,
	}
	for _, shell := range []string{"bash", "zsh", "fish", "pwsh"} {
		t.Run(shell, func(t *testing.T) {
			got := webhookCommandInvocation(invocation, shell)
			require.Equal(t, "go -C "+terminal.ShellQuoter(shell)(directory)+" run ./cmd/openai", got)
			require.NotContains(t, got, "go-build123")
		})
	}
	invocation.goRunDir = filepath.Join(t.TempDir(), "controls\n\t\x1b[31m\u202e")
	for _, shell := range []string{"bash", "zsh", "fish", "pwsh"} {
		got := webhookCommandInvocation(invocation, shell)
		require.NotEmpty(t, got)
		for _, control := range []string{"\n", "\t", "\x1b", "\u202e"} {
			require.NotContains(t, got, control, shell)
		}
	}
}

func TestWebhookCommandGoRunInvocationOmitsUnknownDirectory(t *testing.T) {
	for _, directory := range []string{"", "relative-checkout", filepath.Join(t.TempDir(), "bad\x00path"), filepath.Join(t.TempDir(), "bad\xffpath")} {
		invocation := webhookInvocation{display: "go run ./cmd/openai", goRun: true, goRunDir: directory}
		require.Empty(t, webhookCommandInvocation(invocation, "bash"), strings.ReplaceAll(directory, "\x00", "NUL"))
	}
	require.Empty(t, webhookCommandInvocation(webhookInvocation{goRun: true, goRunDir: t.TempDir()}, "unknown"))
}
