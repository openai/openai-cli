package images

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/autocomplete"
	"github.com/stretchr/testify/require"
)

// Test-only access for presentation checks in the external integration package.
var TestImagePickerShellSetupFailure = imagePickerShellSetupFailure

func TestImagePickerFirstRunNativeSetupContract(t *testing.T) {
	contract := os.Getenv("OPENAI_CLI_PICKER_SETUP_CONTRACT")
	if contract == "" {
		contract = "supported"
	}
	require.Contains(t, []string{"supported", "refused"}, contract)
	for _, existing := range []bool{false, true} {
		name := "absent"
		if existing {
			name = "existing"
		}
		t.Run(name, func(t *testing.T) {
			home := pickerShellSetupHome(t)
			targets := firstRunShellTargets(t, home, "bash")
			original := []byte("# first-use startup\n")
			if existing {
				for _, target := range targets {
					require.NoError(t, os.WriteFile(target.Profile, original, 0600))
				}
			}
			before := firstRunFileSnapshot(t, home)
			// Capture both real streams. Optional setup must never print its
			// explicit-setup diagnostic or a success receipt.
			output, err := os.CreateTemp(t.TempDir(), "first-use-output-")
			require.NoError(t, err)
			previousOut, previousErr := os.Stdout, os.Stderr
			func() {
				os.Stdout, os.Stderr = output, output
				defer func() { os.Stdout, os.Stderr = previousOut, previousErr }()
				err = setupImagePickerFirstRun(t.Context(), targets)
			}()
			require.NoError(t, output.Close())
			if contract == "refused" {
				require.ErrorIs(t, err, autocomplete.ErrPickerAtomicRenameUnsupported)
				for _, target := range targets {
					data, readErr := os.ReadFile(target.Profile)
					if existing {
						require.NoError(t, readErr)
						require.Equal(t, original, data)
						info, statErr := os.Stat(target.Profile)
						require.NoError(t, statErr)
						require.True(t, os.SameFile(before[target.Profile], info))
						require.Equal(t, before[target.Profile].ModTime(), info.ModTime())
					} else {
						require.ErrorIs(t, readErr, os.ErrNotExist)
					}
				}
			} else {
				require.NoError(t, err)
				for _, target := range targets {
					installed, readErr := autocomplete.IsPickerInstalled(t.Context(), target)
					require.NoError(t, readErr)
					require.True(t, installed)
				}
			}
			data, err := os.ReadFile(output.Name())
			require.NoError(t, err)
			require.Empty(t, data)
		})
	}
}

func TestImagePickerFirstRunEligibility(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		env  map[string]string
		tty  [3]bool
		want bool
	}{
		{"help", []string{"openai", "--help"}, nil, [3]bool{true, true, true}, true},
		{"bare command", []string{"openai"}, nil, [3]bool{true, true, true}, true},
		{"normal command", []string{"openai", "images", "generate"}, nil, [3]bool{true, true, true}, true},
		{"piped input", []string{"openai", "--help"}, nil, [3]bool{false, true, true}, false},
		{"redirected output", []string{"openai", "--help"}, nil, [3]bool{true, false, true}, false},
		{"redirected errors", []string{"openai", "--help"}, nil, [3]bool{true, true, false}, false},
		{"dumb terminal", []string{"openai", "--help"}, map[string]string{"TERM": "DUMB"}, [3]bool{true, true, true}, false},
		{"CI", []string{"openai", "--help"}, map[string]string{"CI": "true"}, [3]bool{true, true, true}, false},
		{"CI zero", []string{"openai", "--help"}, map[string]string{"CI": "0"}, [3]bool{true, true, true}, true},
		{"CI false", []string{"openai", "--help"}, map[string]string{"CI": "FALSE"}, [3]bool{true, true, true}, true},
		{"GitHub Actions", []string{"openai", "--help"}, map[string]string{"GITHUB_ACTIONS": "true"}, [3]bool{true, true, true}, false},
		{"Azure Pipelines", []string{"openai", "--help"}, map[string]string{"TF_BUILD": "True"}, [3]bool{true, true, true}, false},
		{"completion", []string{"openai", "__complete", "--help"}, nil, [3]bool{true, true, true}, false},
		{"completion generation", []string{"openai", "@completion", "zsh"}, nil, [3]bool{true, true, true}, false},
		{"completion global flag", []string{"openai", "--format", "text", "@completion", "zsh"}, nil, [3]bool{true, true, true}, false},
		{"manual generation", []string{"openai", "@manpages"}, nil, [3]bool{true, true, true}, false},
		{"local command search", []string{"openai", "explore"}, nil, [3]bool{true, true, true}, false},
		{"local command search global flag", []string{"openai", "--format", "text", "explore"}, nil, [3]bool{true, true, true}, false},
		{"Tab key invocation", []string{"openai", "images", "generate"}, map[string]string{"OPENAI_PICKER_SHELL": "pwsh"}, [3]bool{true, true, true}, false},
		{"active integration can need an upgrade", []string{"openai", "--help"}, map[string]string{"OPENAI_PICKER_INTEGRATION": "zsh"}, [3]bool{true, true, true}, true},
		{"inherited other shell integration", []string{"openai", "--help"}, map[string]string{"OPENAI_PICKER_INTEGRATION": "fish"}, [3]bool{true, true, true}, true},
		{"no argv", nil, nil, [3]bool{true, true, true}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, FirstRunEligible(test.args, func(name string) string { return test.env[name] }, test.tty[0], test.tty[1], test.tty[2], "zsh"))
		})
	}
	require.False(t, FirstRunEligible([]string{"openai", "--help"}, func(string) string { return "" }, true, true, true, ""))
	require.False(t, FirstRunEligible([]string{"openai", "--help"}, func(string) string { return "" }, true, true, true, "pwsh"))
}

func TestImagePickerFirstRunRequiresExactPathExecutable(t *testing.T) {
	directory := t.TempDir()
	current, other := filepath.Join(directory, "current"), filepath.Join(directory, "other")
	require.NoError(t, os.WriteFile(current, []byte("same binary contents"), 0700))
	require.NoError(t, os.WriteFile(other, []byte("same binary contents"), 0700))
	require.True(t, imagePickerSameExecutable(current, current))
	require.False(t, imagePickerSameExecutable(current, other), "identical bytes in another executable do not identify this installation")
	require.False(t, imagePickerSameExecutable(current, directory))
	require.False(t, imagePickerSameExecutable(current, filepath.Join(directory, "missing")))
	if runtime.GOOS != "windows" {
		link := filepath.Join(directory, "openai")
		require.NoError(t, os.Symlink(current, link))
		require.True(t, imagePickerSameExecutable(current, link), "package manager executable symlinks identify the same installation")
	}
}

func firstRunShellTargets(t *testing.T, home, shell string) []autocomplete.PickerInstallation {
	t.Helper()
	targets, err := imagePickerSelectShellTargets(shell, "", imagePickerShellPaths{
		home: home, config: filepath.Join(home, "config"), zdotdir: home,
	})
	require.NoError(t, err)
	return targets
}

func firstRunFileSnapshot(t *testing.T, directory string) map[string]os.FileInfo {
	t.Helper()
	result := map[string]os.FileInfo{}
	require.NoError(t, filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		result[path] = info
		return err
	}))
	return result
}

func TestImagePickerFirstRunSetupAndRepeatedRunDoNotRewrite(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			home := pickerShellSetupHome(t)
			targets := firstRunShellTargets(t, home, shell)
			for _, target := range targets {
				require.NoError(t, os.MkdirAll(filepath.Dir(target.Profile), 0700))
				require.NoError(t, os.WriteFile(target.Profile, []byte("# personal startup\n"), 0600))
			}
			require.NoError(t, setupImagePickerFirstRun(t.Context(), targets))
			for _, target := range targets {
				installed, err := autocomplete.IsPickerInstalled(t.Context(), target)
				require.NoError(t, err)
				require.True(t, installed)
				profile, err := os.ReadFile(target.Profile)
				require.NoError(t, err)
				require.True(t, strings.HasPrefix(string(profile), "# personal startup\n"))
			}
			before := firstRunFileSnapshot(t, home)
			require.NoError(t, setupImagePickerFirstRun(t.Context(), targets))
			after := firstRunFileSnapshot(t, home)
			require.Len(t, after, len(before))
			for path, info := range before {
				require.True(t, os.SameFile(info, after[path]))
				require.Equal(t, info.ModTime(), after[path].ModTime())
			}
		})
	}
}

func TestImagePickerFirstRunSkipsPowerShellWithoutWriting(t *testing.T) {
	home := pickerShellSetupHome(t)
	targets := []autocomplete.PickerInstallation{{Shell: autocomplete.CompletionStylePowershell, Directory: filepath.Join(home, "config"), Profile: filepath.Join(home, "profile.ps1")}}
	before := firstRunFileSnapshot(t, home)
	require.NoError(t, setupImagePickerFirstRun(t.Context(), targets))
	after := firstRunFileSnapshot(t, home)
	require.Len(t, after, len(before))
	for path, info := range before {
		require.True(t, os.SameFile(info, after[path]))
		require.Equal(t, info.ModTime(), after[path].ModTime())
	}
}

func TestImagePickerFirstRunPreservesOptOutAndUntrustedProfile(t *testing.T) {
	for _, mode := range []string{"optout", "modified profile", "symlink profile", "unsafe configuration"} {
		t.Run(mode, func(t *testing.T) {
			if runtime.GOOS == "windows" && (mode == "symlink profile" || mode == "unsafe configuration") {
				t.Skip("Unix symlink fixture")
			}
			home := pickerShellSetupHome(t)
			targets := firstRunShellTargets(t, home, "bash")
			if mode == "optout" {
				require.NoError(t, declineImagePickerTab(t.Context(), "bash"))
			} else if mode == "unsafe configuration" {
				configuration := filepath.Dir(filepath.Dir(targets[0].Directory))
				require.NoError(t, os.MkdirAll(configuration, 0700))
				require.NoError(t, os.Chmod(configuration, 0770))
			} else if mode == "modified profile" {
				require.NoError(t, os.WriteFile(targets[1].Profile, []byte("# >>> openai image picker damaged\n"), 0600))
			} else {
				original := filepath.Join(home, "original")
				require.NoError(t, os.WriteFile(original, []byte("# untouched startup\n"), 0600))
				require.NoError(t, os.Symlink(original, targets[1].Profile))
			}
			before := firstRunFileSnapshot(t, home)
			err := setupImagePickerFirstRun(t.Context(), targets)
			if mode == "optout" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			after := firstRunFileSnapshot(t, home)
			require.Len(t, after, len(before), "preflight must inspect all profiles before creating state")
			for path, info := range before {
				require.True(t, os.SameFile(info, after[path]))
				require.Equal(t, info.ModTime(), after[path].ModTime())
			}
		})
	}
}

func TestImagePickerFirstRunContendedLockHonorsDeadline(t *testing.T) {
	home := pickerShellSetupHome(t)
	targets := firstRunShellTargets(t, home, "zsh")
	choice, err := imagePickerTabChoicePath("zsh")
	require.NoError(t, err)
	locked, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- autocomplete.WithPickerSetupLock(t.Context(), filepath.Dir(choice), func() error {
			close(locked)
			<-release
			return nil
		})
	}()
	select {
	case <-locked:
	case <-time.After(3 * time.Second):
		t.Fatal("could not acquire fixture setup lock")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = setupImagePickerFirstRun(ctx, targets)
	close(release)
	require.NoError(t, <-done)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(started), 2*time.Second, "ordinary commands must not wait for another shell setup")
	_, err = os.Stat(targets[0].Profile)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestImagePickerFirstRunUnwritableDirectoriesPreserveProfile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires an unprivileged Unix account for permission denial")
	}
	for _, protected := range []string{"profile directory", "configuration directory"} {
		t.Run(protected, func(t *testing.T) {
			home := pickerShellSetupHome(t)
			profileDirectory := filepath.Join(home, "startup")
			configuration := filepath.Join(home, "config")
			for _, path := range []string{profileDirectory, configuration} {
				require.NoError(t, os.Mkdir(path, 0700))
			}
			profile := filepath.Join(profileDirectory, ".zshrc")
			original := []byte("# protected personal startup\n")
			require.NoError(t, os.WriteFile(profile, original, 0600))
			targets, err := imagePickerSelectShellTargets("zsh", profile, imagePickerShellPaths{home: home, config: configuration})
			require.NoError(t, err)
			blocked := profileDirectory
			if protected == "configuration directory" {
				blocked = configuration
			}
			require.NoError(t, os.Chmod(blocked, 0500))
			t.Cleanup(func() { require.NoError(t, os.Chmod(blocked, 0700)) })
			require.Error(t, setupImagePickerFirstRun(t.Context(), targets))
			got, err := os.ReadFile(profile)
			require.NoError(t, err)
			require.Equal(t, original, got)
		})
	}
}

func TestImagePickerFirstRunConcurrentSetupAndCanceledRun(t *testing.T) {
	home := pickerShellSetupHome(t)
	targets := firstRunShellTargets(t, home, "zsh")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, setupImagePickerFirstRun(ctx, targets), context.Canceled)
	entries, err := os.ReadDir(home)
	require.NoError(t, err)
	require.Empty(t, entries)
	ready, done := make(chan struct{}), make(chan error, 2)
	for range 2 {
		go func() {
			<-ready
			done <- setupImagePickerFirstRun(t.Context(), targets)
		}()
	}
	close(ready)
	require.NoError(t, <-done)
	require.NoError(t, <-done)
	profile, err := os.ReadFile(targets[0].Profile)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(profile), "# >>> openai image picker"))
}
