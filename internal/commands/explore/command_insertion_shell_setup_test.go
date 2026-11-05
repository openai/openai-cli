package explore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/autocomplete"
	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-cli/internal/commands/images"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func insertionSetupTestHome(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("native Windows CLI has no command-capture transport")
	}
	home := pickerShellSetupHome(t)
	bin := filepath.Join(home, "bin")
	require.NoError(t, os.Mkdir(bin, 0700))
	running, err := os.Executable()
	require.NoError(t, err)
	name := "openai"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	// This test does not execute the launcher. Its identity is needed for setup.
	require.NoError(t, os.Link(running, filepath.Join(bin, name)))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return home
}

func TestCommandInsertionWindowsSetupRefusesBeforeWriting(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("requires the native Windows CLI")
	}
	home := pickerShellSetupHome(t)
	err := runInsertionShellSetup(t, io.Discard, "@completion", "bash", "--install-suggestions")
	require.ErrorContains(t, err, "use the Linux CLI inside WSL")
	entries, err := os.ReadDir(home)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func runInsertionShellSetup(t *testing.T, out io.Writer, args ...string) error {
	t.Helper()
	root := &cli.Command{Name: "openai", Writer: out, ErrWriter: io.Discard,
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Flags:          []cli.Flag{&cli.StringFlag{Name: "format"}, &cli.StringFlag{Name: "transform"}, &cli.BoolFlag{Name: "raw-output"}},
		Commands:       []*cli.Command{{Name: "@completion", Action: autocomplete.OutputCompletionScript}},
	}
	images.ConfigureCompletion(root)
	images.ConfigureShellSetup(root, func(context.Context, string) error { return nil })
	ConfigureCompletion(root)
	return root.Run(t.Context(), append([]string{"openai"}, args...))
}

func TestCommandInsertionSetupInstallStatusRemove(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			home := insertionSetupTestHome(t)
			profile := filepath.Join(home, "startup with spaces")
			original := []byte("# personal settings\n")
			require.NoError(t, os.WriteFile(profile, original, 0600))
			optOut := filepath.Join(home, ".openai", "shell", "image-picker.tab-off-"+shell)
			require.NoError(t, os.MkdirAll(filepath.Dir(optOut), 0700))
			require.NoError(t, os.WriteFile(optOut, []byte{}, 0600))
			for iteration := range 2 {
				var out bytes.Buffer
				require.NoError(t, runInsertionShellSetup(t, &out, "--format", "json", "@completion", shell, "--install-suggestions", "--profile", profile, "--suggestions-double-tab"))
				var status commandInsertionSetupResult
				require.NoError(t, json.Unmarshal(out.Bytes(), &status))
				require.Equal(t, 1, status.Installed)
				require.Equal(t, iteration == 0, status.Changed)
				require.True(t, status.DoubleTab)
				data, err := os.ReadFile(profile)
				require.NoError(t, err)
				require.True(t, bytes.HasPrefix(data, original))
				require.Equal(t, 1, strings.Count(string(data), "# >>> openai command insertion"))
				require.NotContains(t, string(data), "# >>> openai image picker")
				out.Reset()
				require.NoError(t, runInsertionShellSetup(t, &out, "--format", "jsonl", "@completion", shell, "--suggestions-status", "--profile", profile))
				require.NoError(t, json.Unmarshal(out.Bytes(), &status))
				require.Equal(t, "status", status.Action)
				require.True(t, status.DoubleTab)
				kept, err := os.ReadFile(optOut)
				require.NoError(t, err)
				require.Empty(t, kept)
			}
			for range 2 {
				require.NoError(t, runInsertionShellSetup(t, io.Discard, "@completion", shell, "--uninstall-suggestions", "--profile", profile))
				data, err := os.ReadFile(profile)
				require.NoError(t, err)
				require.Equal(t, original, data)
			}
		})
	}
}

func TestCommandInsertionStatusDoesNotCreateFiles(t *testing.T) {
	home := pickerShellSetupHome(t)
	for _, shell := range []string{"bash", "zsh", "fish"} {
		var output bytes.Buffer
		require.NoError(t, runInsertionShellSetup(t, &output, "--format", "json", "@completion", shell, "--suggestions-status"))
		var result commandInsertionSetupResult
		require.NoError(t, json.Unmarshal(output.Bytes(), &result))
		require.Zero(t, result.Installed)
	}
	entries, err := os.ReadDir(home)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestCommandInsertionSetupRejectsConflictingActionsBeforeWriting(t *testing.T) {
	for _, flags := range [][]string{
		{"--install-suggestions", "--uninstall-suggestions"},
		{"--install-suggestions", "--suggestions-status"},
		{"--install-suggestions", "--picker"},
		{"--install-suggestions", "--install-picker"},
		{"--install-suggestions", "--suggestions-click"},
		{"--suggestions-status", "--suggestions-double-tab"},
		{"--install-suggestions", "--automatic"},
		{"--install-suggestions", "--suggestions-adapter", "kitty"},
	} {
		t.Run(strings.Join(flags, "_"), func(t *testing.T) {
			home := pickerShellSetupHome(t)
			err := runInsertionShellSetup(t, io.Discard, append([]string{"@completion", "zsh"}, flags...)...)
			require.ErrorContains(t, err, "Choose one command insertion setup action")
			entries, err := os.ReadDir(home)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestCommandInsertionSetupZshRequiresActualProfile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native Windows installation is refused before profile discovery")
	}
	home := pickerShellSetupHome(t)
	t.Setenv("ZDOTDIR", "")
	err := runInsertionShellSetup(t, io.Discard, "@completion", "zsh", "--install-suggestions")
	require.ErrorContains(t, err, `--profile "${ZDOTDIR-$HOME}/.zshrc"`)
	entries, err := os.ReadDir(home)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestCommandInsertionShellTargetsKeepFishSeparate(t *testing.T) {
	home := pickerShellSetupHome(t)
	targets, err := commandInsertionShellTargets(t.Context(), "fish", "", false)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Equal(t, filepath.Join(home, "config", "fish", "conf.d", "openai-suggestions.fish"), targets[0].Profile)
}

func TestCommandInsertionExecutableKeepsLogicalUpgradePath(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "v1")
	second := filepath.Join(root, "v2")
	require.NoError(t, os.WriteFile(first, []byte("v1"), 0700))
	require.NoError(t, os.WriteFile(second, []byte("v2"), 0700))
	launcher := filepath.Join(root, "openai")
	if err := os.Symlink(first, launcher); err != nil && runtime.GOOS == "windows" {
		t.Skip("symlink privilege unavailable")
	} else {
		require.NoError(t, err)
	}
	got, err := verifyCommandInsertionExecutable(first, launcher)
	require.NoError(t, err)
	require.Equal(t, launcher, got)
	require.NoError(t, os.Remove(launcher))
	require.NoError(t, os.Symlink(second, launcher))
	got, err = verifyCommandInsertionExecutable(second, launcher)
	require.NoError(t, err)
	require.Equal(t, launcher, got)
	_, err = verifyCommandInsertionExecutable(first, launcher)
	require.Error(t, err)
	_, err = verifyCommandInsertionExecutable(second, "./openai")
	require.Error(t, err)
}

type insertionShortWriter struct{}

func (insertionShortWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }

func TestCommandInsertionSetupReportsOutputFailureAfterCommit(t *testing.T) {
	home := insertionSetupTestHome(t)
	profile := filepath.Join(home, ".zshrc")
	err := runInsertionShellSetup(t, insertionShortWriter{}, "@completion", "zsh", "--install-suggestions", "--profile", profile)
	require.ErrorIs(t, err, io.ErrShortWrite)
	require.ErrorContains(t, err, "finished, but its result could not be displayed")
	var result bytes.Buffer
	require.NoError(t, runInsertionShellSetup(t, &result, "--format", "json", "@completion", "zsh", "--suggestions-status", "--profile", profile))
	var status commandInsertionSetupResult
	require.NoError(t, json.Unmarshal(result.Bytes(), &status))
	require.Equal(t, 1, status.Installed)
}

func TestCommandInsertionSetupFailureKeepsCancellation(t *testing.T) {
	err := insertionSetupFailure("fixed setup message", context.Canceled, 1)
	require.ErrorIs(t, err, context.Canceled)
	var exit cli.ExitCoder
	require.True(t, errors.As(err, &exit))
	require.Equal(t, 130, exit.ExitCode())
}

func TestCommandInsertionSetupPresenterKeepsFixedPrivateMessage(t *testing.T) {
	for _, cause := range []error{&os.PathError{Op: "write", Path: "/private/user/startup", Err: os.ErrPermission}, context.Canceled, context.DeadlineExceeded} {
		failure := insertionSetupFailure("Command insertion setup was interrupted.", cause, 1)
		var local *localcommands.LocalError
		require.ErrorAs(t, failure, &local)
		require.Equal(t, "Command insertion setup was interrupted.", local.Message)
		require.ErrorIs(t, failure, cause)
		require.NotContains(t, failure.Error(), "/private/user")
	}
}

func pickerShellSetupHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("APPDATA", filepath.Join(home, "config"))
	t.Setenv("ZDOTDIR", home)
	return home
}
