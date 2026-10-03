package custom

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/autocomplete"
	"github.com/stretchr/testify/require"
)

func TestImagePickerFirstRunBlockedWorkDoesNotBlockCommand(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "cancellation"}[canceled], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			started, unblock, foregroundDone, workerDone := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			var release sync.Once
			t.Cleanup(func() { release.Do(func() { close(unblock) }) })
			workerContext := make(chan context.Context, 1)
			go func() {
				waitForImagePickerFirstRun(ctx, func(ctx context.Context) {
					workerContext <- ctx
					close(started)
					<-unblock // Models an OS operation that ignores context cancellation.
					close(workerDone)
				})
				close(foregroundDone)
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("setup worker did not start")
			}
			if canceled {
				cancel()
			}
			select {
			case <-foregroundDone:
			case <-time.After(2 * time.Second):
				t.Fatal("optional blocked I/O prevented the foreground command from continuing")
			}
			require.Error(t, (<-workerContext).Err())
			secondStarted := make(chan struct{}, 1)
			waitForImagePickerFirstRun(t.Context(), func(context.Context) { secondStarted <- struct{}{} })
			require.Empty(t, secondStarted, "a blocked worker must not accumulate more work")
			release.Do(func() { close(unblock) })
			<-workerDone
			// The worker releases admission after its callback returns.
			require.Eventually(t, func() bool { return len(imagePickerFirstRunWorker) == 0 }, time.Second, time.Millisecond)
			waitForImagePickerFirstRun(t.Context(), func(context.Context) { secondStarted <- struct{}{} })
			require.Len(t, secondStarted, 1, "finished work must release the slot")
		})
	}
}

func TestImagePickerFirstRunAlreadyCanceledDoesNotStartWork(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	started := make(chan struct{}, 1)
	waitForImagePickerFirstRun(ctx, func(context.Context) { started <- struct{}{} })
	require.Empty(t, started)
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
		{"Tab key invocation", []string{"openai", "images", "generate"}, map[string]string{"OPENAI_PICKER_SHELL": "pwsh"}, [3]bool{true, true, true}, false},
		{"active integration can need an upgrade", []string{"openai", "--help"}, map[string]string{"OPENAI_PICKER_INTEGRATION": "zsh"}, [3]bool{true, true, true}, true},
		{"inherited other shell integration", []string{"openai", "--help"}, map[string]string{"OPENAI_PICKER_INTEGRATION": "fish"}, [3]bool{true, true, true}, true},
		{"no argv", nil, nil, [3]bool{true, true, true}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, imagePickerFirstRunEligible(test.args, func(name string) string { return test.env[name] }, test.tty[0], test.tty[1], test.tty[2], "zsh"))
		})
	}
	require.False(t, imagePickerFirstRunEligible([]string{"openai", "--help"}, func(string) string { return "" }, true, true, true, ""))
	require.False(t, imagePickerFirstRunEligible([]string{"openai", "--help"}, func(string) string { return "" }, true, true, true, "pwsh"))
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
		home: home, config: filepath.Join(home, "config"),
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
	for _, mode := range []string{"optout", "modified profile", "symlink profile"} {
		t.Run(mode, func(t *testing.T) {
			if runtime.GOOS == "windows" && mode == "symlink profile" {
				t.Skip("Unix symlink fixture")
			}
			home := pickerShellSetupHome(t)
			targets := firstRunShellTargets(t, home, "bash")
			if mode == "optout" {
				require.NoError(t, declineImagePickerTab(t.Context(), "bash"))
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
	locked, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- autocomplete.WithPickerSetupLock(t.Context(), targets[0].Directory, func() error {
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
	ctx, cancel := context.WithTimeout(t.Context(), imagePickerFirstRunTimeout)
	defer cancel()
	started := time.Now()
	err := setupImagePickerFirstRun(ctx, targets)
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
