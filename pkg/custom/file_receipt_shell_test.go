package custom

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestFileReceiptShellCrossesOnlyRecognizedGoLauncher(t *testing.T) {
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
			got := fileReceiptShellName(t.Context(), tc.goRun, tc.parent, func() (string, error) {
				lookups++
				return tc.caller, nil
			})
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.lookups, lookups)
		})
	}
}

func TestFileReceiptShellLookupFailureAndCancellation(t *testing.T) {
	t.Setenv("SHELL", "/bin/bash")
	t.Run("lookup failure does not guess", func(t *testing.T) {
		got := fileReceiptShellName(t.Context(), true, "go", func() (string, error) {
			return "bash", errors.New("synthetic unavailable parent")
		})
		require.Empty(t, got)
	})
	t.Run("already canceled performs no lookup", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		got := fileReceiptShellName(ctx, true, "go", func() (string, error) {
			t.Fatal("canceled receipt inspected another process")
			return "bash", nil
		})
		require.Empty(t, got)
		require.Empty(t, fileReceiptShell(ctx, fileInvocation{goRun: true}))
	})
	t.Run("canceled lookup discards result", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		got := fileReceiptShellName(ctx, true, "go", func() (string, error) {
			cancel()
			return "bash", nil
		})
		require.Empty(t, got)
	})
	t.Run("unsafe request context omits lookup", func(t *testing.T) {
		require.Empty(t, fileReceiptShell(t.Context(), fileInvocation{goRun: true, omitHint: true}))
	})
}

func TestFileReceiptGoRunInvocationCapturesCheckout(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "checkout's folder")
	invocation := fileInvocation{
		display: "go run ./cmd/openai", executable: "/tmp/go-build123/b001/exe/openai",
		goRun: true, goRunDir: directory,
	}
	for _, shell := range []string{"bash", "zsh", "fish", "pwsh"} {
		t.Run(shell, func(t *testing.T) {
			got := fileReceiptInvocation(invocation, shell)
			require.Equal(t, "go -C "+imagePickerShellQuoter(shell)(directory)+" run ./cmd/openai", got)
			require.NotContains(t, got, "go-build123")
		})
	}
	invocation.goRunDir = filepath.Join(t.TempDir(), "controls\n\t\x1b[31m\u202e")
	for _, shell := range []string{"bash", "zsh", "fish", "pwsh"} {
		got := fileReceiptInvocation(invocation, shell)
		require.NotEmpty(t, got)
		for _, control := range []string{"\n", "\t", "\x1b", "\u202e"} {
			require.NotContains(t, got, control, shell)
		}
	}
}

func TestFileReceiptGoRunInvocationOmitsUnknownDirectory(t *testing.T) {
	for _, directory := range []string{"", "relative-checkout", filepath.Join(t.TempDir(), "bad\x00path"), filepath.Join(t.TempDir(), "bad\xffpath")} {
		invocation := fileInvocation{display: "go run ./cmd/openai", goRun: true, goRunDir: directory}
		require.Empty(t, fileReceiptInvocation(invocation, "bash"), strings.ReplaceAll(directory, "\x00", "NUL"))
	}
	require.Empty(t, fileReceiptInvocation(fileInvocation{goRun: true, goRunDir: t.TempDir()}, "unknown"))
}

func TestFileReceiptGoRunHintPreservesShellArguments(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "pwsh"} {
		t.Run(shell, func(t *testing.T) {
			binary, err := exec.LookPath(shell)
			if err != nil {
				t.Skipf("%s is unavailable", shell)
			}
			work := t.TempDir()
			directory := filepath.Join(work, "checkout's folder\nwith\ttab")
			requestArgs := []string{"--project=project's value", "--organization=org two", "--base-url=http://127.0.0.1:1/v1"}
			invocation := fileInvocation{
				display: "go run ./cmd/openai", executable: "/tmp/go-build123/b001/exe/openai",
				goRun: true, goRunDir: directory, requestArgs: requestArgs,
			}
			var receipt bytes.Buffer
			require.NoError(t, writeFileReceipt(&receipt, gjson.Parse(fileReceiptFixture), shell, invocation))
			_, command, found := strings.Cut(receipt.String(), "\nInspect it: ")
			require.True(t, found, receipt.String())
			prefix := `go() { printf '%s\0' "$@"; }; `
			args := []string{"--noprofile", "--norc", "-c"}
			switch shell {
			case "zsh":
				args = []string{"-f", "-c"}
			case "fish":
				prefix = `function go; printf '%s\0' $argv; end; `
				args = []string{"--no-config", "-c"}
			case "pwsh":
				prefix = `function go { foreach ($item in $args) { [Console]::Out.Write([string]$item); [Console]::Out.Write([char]0) } }; `
				args = []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			process := exec.CommandContext(ctx, binary, append(args, prefix+strings.TrimSpace(command))...)
			process.Dir = work
			for _, entry := range os.Environ() {
				name, _, _ := strings.Cut(entry, "=")
				if !strings.HasPrefix(name, "OPENAI_") && name != "BASH_ENV" && name != "ENV" && name != "HOME" && name != "ZDOTDIR" {
					process.Env = append(process.Env, entry)
				}
			}
			process.Env = append(process.Env, "HOME="+work, "ZDOTDIR="+work)
			output, err := process.CombinedOutput()
			require.NoError(t, ctx.Err(), "copied Go invocation timed out")
			require.NoError(t, err, string(output))
			require.True(t, bytes.HasSuffix(output, []byte{0}), "missing argument delimiter: %q", output)
			arguments := strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
			want := append([]string{"-C", directory, "run", "./cmd/openai"}, requestArgs...)
			require.Equal(t, append(want, "files", "get", "file-example"), arguments)
		})
	}
}
