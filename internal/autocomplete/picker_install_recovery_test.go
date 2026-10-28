package autocomplete

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPickerProfileRecoveryRetainsHeldEditorFile(t *testing.T) {
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish} {
		for _, action := range []string{"replace", "remove"} {
			t.Run(string(shell)+"/"+action, func(t *testing.T) {
				options := pickerInstallFixture(t, shell)
				if action == "replace" {
					require.NoError(t, os.WriteFile(options.Profile, []byte("# personal startup\n"), 0600))
				} else {
					_, err := InstallPicker(t.Context(), options)
					require.NoError(t, err)
				}
				parent, err := os.OpenRoot(filepath.Dir(options.Profile))
				require.NoError(t, err)
				defer parent.Close()
				// Root.OpenFile permits deletion sharing on Windows so this
				// editor can retain its handle while the profile is captured.
				editor, err := parent.OpenFile(filepath.Base(options.Profile), os.O_RDWR, 0)
				require.NoError(t, err)
				defer editor.Close()
				original, err := editor.Stat()
				require.NoError(t, err)
				before, err := os.ReadFile(options.Profile)
				require.NoError(t, err)
				if action == "replace" {
					_, err = InstallPicker(t.Context(), options)
				} else {
					_, err = RemovePicker(t.Context(), options)
				}
				require.NoError(t, err)
				retained := pickerRecoveryFindOriginal(t, options.Profile, original)
				require.NotEmpty(t, retained, "the exact original inode must remain recoverable")
				preserved, err := os.ReadFile(retained)
				require.NoError(t, err)
				require.Equal(t, before, preserved)
				if runtime.GOOS != "windows" {
					info, err := os.Stat(filepath.Dir(retained))
					require.NoError(t, err)
					require.Equal(t, os.FileMode(0700), info.Mode().Perm(), "recovery copies require a private directory")
				}
				// An editor that kept its original handle can finish writing after
				// the CLI has returned. Keep that inode instead of deleting a copy
				// merely because it matched the earlier snapshot.
				late := []byte("# editor completed a held-file save\n")
				_, err = editor.WriteAt(late, 0)
				require.NoError(t, err)
				require.NoError(t, editor.Truncate(int64(len(late))))
				require.NoError(t, editor.Sync())
				preserved, err = os.ReadFile(retained)
				require.NoError(t, err)
				require.Equal(t, late, preserved)
				if action == "replace" {
					current, err := os.ReadFile(options.Profile)
					require.NoError(t, err)
					require.True(t, bytes.HasPrefix(current, before))
					require.Contains(t, string(current), pickerBlockBegin)
					require.NotContains(t, string(current), string(late))
				} else {
					_, err := os.Lstat(options.Profile)
					require.ErrorIs(t, err, os.ErrNotExist)
				}
			})
		}
	}
}

func TestPickerProfileRecoveryPreservesCompetingPublication(t *testing.T) {
	for _, action := range []string{"replace", "remove"} {
		t.Run(action, func(t *testing.T) {
			options, original, before := pickerRecoveryFixture(t, action)
			competing := []byte("# independently published profile\n")
			ctx := &pickerRecoveryCaptureContext{Context: t.Context(), profile: options.Profile, original: original,
				action: func() error { return os.WriteFile(options.Profile, competing, 0600) }}
			_, err := pickerRecoveryChange(ctx, options, action)
			require.True(t, ctx.fired, "exercise the boundary after capture and before publication")
			require.NoError(t, ctx.actionErr)
			if action == "replace" {
				require.Error(t, err, "exclusive publication must not replace the competing profile")
			}
			current, err := os.ReadFile(options.Profile)
			require.NoError(t, err)
			require.Equal(t, competing, current)
			retained := pickerRecoveryFindOriginal(t, options.Profile, original)
			require.NotEmpty(t, retained)
			preserved, err := os.ReadFile(retained)
			require.NoError(t, err)
			require.Equal(t, before, preserved)
		})
	}
}

func TestPickerProfileRecoveryCancellationRestoresOnRetry(t *testing.T) {
	for _, action := range []string{"replace", "remove"} {
		t.Run(action, func(t *testing.T) {
			options, original, before := pickerRecoveryFixture(t, action)
			ctx := &pickerRecoveryCaptureContext{Context: t.Context(), profile: options.Profile, original: original,
				action: func() error { return context.Canceled }}
			_, err := pickerRecoveryChange(ctx, options, action)
			require.True(t, ctx.fired)
			require.ErrorIs(t, err, context.Canceled)
			current, readErr := os.ReadFile(options.Profile)
			if errors.Is(readErr, os.ErrNotExist) {
				retained := pickerRecoveryFindOriginal(t, options.Profile, original)
				require.NotEmpty(t, retained)
				current, readErr = os.ReadFile(retained)
			}
			require.NoError(t, readErr)
			require.Equal(t, before, current)
			_, err = pickerRecoveryChange(t.Context(), options, action)
			require.NoError(t, err, "fresh operation must recover an interrupted capture")
			retained := pickerRecoveryFindOriginal(t, options.Profile, original)
			require.NotEmpty(t, retained)
			preserved, err := os.ReadFile(retained)
			require.NoError(t, err)
			require.Equal(t, before, preserved)
		})
	}
}

func TestPickerProfileRecoveryMarkerFailureKeepsPublishedScript(t *testing.T) {
	options, original, before := pickerRecoveryFixture(t, "replace")
	ctx := &pickerRecoveryCaptureContext{Context: t.Context(), profile: options.Profile, original: original,
		action: func() error {
			retained := pickerRecoveryFindOriginal(t, options.Profile, original)
			return os.Mkdir(filepath.Join(filepath.Dir(retained), "complete"), 0700)
		}}
	result, err := InstallPicker(ctx, options)
	require.True(t, ctx.fired)
	require.NoError(t, ctx.actionErr)
	require.Error(t, err, "recording completion must fail after publication")
	current, err := os.ReadFile(options.Profile)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(current, before))
	require.Contains(t, string(current), pickerBlockBegin)
	script, err := os.ReadFile(result.ScriptPath)
	require.NoError(t, err, "a reported completion failure must not remove the script already sourced by the published profile")
	require.True(t, bytes.HasPrefix(script, []byte(pickerScriptHeader)))
	active, err := IsPickerInstalled(t.Context(), options)
	require.NoError(t, err)
	require.True(t, active)
}

func TestPickerProfileRecoveryDetectsLateEditorSave(t *testing.T) {
	for _, mode := range []string{"replace atomic", "remove atomic", "remove in-place"} {
		t.Run(mode, func(t *testing.T) {
			action := strings.SplitN(mode, " ", 2)[0]
			options, _, _ := pickerRecoveryFixture(t, action)
			edited := []byte("# late editor save\n")
			ctx := &pickerRecoveryBeforeCaptureContext{Context: t.Context(), profile: options.Profile, replace: action == "replace",
				action: func() error {
					if strings.HasSuffix(mode, "in-place") {
						return os.WriteFile(options.Profile, edited, 0600)
					}
					temporary := options.Profile + ".editor"
					if err := os.WriteFile(temporary, edited, 0600); err != nil {
						return err
					}
					return os.Rename(temporary, options.Profile)
				}}
			_, err := pickerRecoveryChange(ctx, options, action)
			require.True(t, ctx.fired, "inject after preparing recovery and before capturing the profile")
			require.NoError(t, ctx.actionErr)
			require.Error(t, err)
			current, err := os.ReadFile(options.Profile)
			require.NoError(t, err)
			require.Equal(t, edited, current, "restore the captured editor save without overwriting a competing path")
		})
	}
}

func TestPickerProfileRecoveryInterruptedCapture(t *testing.T) {
	const childEnv = "OPENAI_CLI_TEST_PICKER_RECOVERY_CRASH"
	type request struct {
		Options PickerInstallation
		Action  string
	}
	if value := os.Getenv(childEnv); value != "" {
		var child request
		require.NoError(t, json.Unmarshal([]byte(value), &child))
		original, err := pickerRecoveryFileIdentity(child.Options.Profile)
		require.NoError(t, err)
		ctx := &pickerRecoveryCaptureContext{Context: t.Context(), profile: child.Options.Profile, original: original,
			action: func() error { os.Exit(77); return nil }}
		_, err = pickerRecoveryChange(ctx, child.Options, child.Action)
		t.Fatalf("capture interruption was not reached: %v", err)
	}
	for _, action := range []string{"replace", "remove"} {
		t.Run(action, func(t *testing.T) {
			options, original, before := pickerRecoveryFixture(t, action)
			requestBytes, err := json.Marshal(request{options, action})
			require.NoError(t, err)
			deadline, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			child := exec.CommandContext(deadline, os.Args[0], "-test.run=^TestPickerProfileRecoveryInterruptedCapture$")
			child.Env = append(os.Environ(), childEnv+"="+string(requestBytes))
			output, err := child.CombinedOutput()
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit, "%s", output)
			require.Equal(t, 77, exit.ExitCode(), "%s", output)
			_, err = os.Lstat(options.Profile)
			require.ErrorIs(t, err, os.ErrNotExist, "child exited without running recovery defers")
			retained := pickerRecoveryFindOriginal(t, options.Profile, original)
			require.NotEmpty(t, retained)
			preserved, err := os.ReadFile(retained)
			require.NoError(t, err)
			require.Equal(t, before, preserved)
			_, err = pickerRecoveryChange(t.Context(), options, action)
			require.NoError(t, err, "next process must recover the pending capture")
			retained = pickerRecoveryFindOriginal(t, options.Profile, original)
			require.NotEmpty(t, retained)
			preserved, err = os.ReadFile(retained)
			require.NoError(t, err)
			require.Equal(t, before, preserved)
			active, err := IsPickerInstalled(t.Context(), options)
			require.NoError(t, err)
			require.Equal(t, action == "replace", active)
		})
	}
}

func TestPickerProfileRecoveryPendingOtherSpellingRefusesNewProfile(t *testing.T) {
	for _, kind := range []string{"different profile", "case alias"} {
		t.Run(kind, func(t *testing.T) {
			options := pickerInstallFixture(t, CompletionStyleZsh)
			options.Profile = filepath.Join(filepath.Dir(options.Profile), "MiXeDProfile.rc")
			personal := []byte("# personal content before interrupted setup\n")
			require.NoError(t, os.WriteFile(options.Profile, personal, 0600))
			original, err := pickerRecoveryFileIdentity(options.Profile)
			require.NoError(t, err)
			other := options
			other.Profile = filepath.Join(filepath.Dir(options.Profile), "other-profile")
			if kind == "case alias" {
				other.Profile = filepath.Join(filepath.Dir(options.Profile), "mixedprofile.rc")
				alias, err := pickerRecoveryFileIdentity(other.Profile)
				if errors.Is(err, os.ErrNotExist) {
					t.Skip("fixture filesystem treats case spellings as distinct files")
				}
				require.NoError(t, err)
				require.True(t, os.SameFile(original, alias))
			}
			pickerRecoveryInterruptAfterCapture(t, options)
			_, err = InstallPicker(t.Context(), other)
			require.Error(t, err, "a different spelling must not bypass recovery while the recorded profile is absent")
			for _, path := range []string{options.Profile, other.Profile} {
				_, err = os.Lstat(path)
				require.ErrorIs(t, err, os.ErrNotExist, "refusal must not create an empty-profile replacement")
			}
			retained := pickerRecoveryFindOriginal(t, options.Profile, original)
			require.NotEmpty(t, retained)
			preserved, err := os.ReadFile(retained)
			require.NoError(t, err)
			require.Equal(t, personal, preserved)
			_, err = InstallPicker(t.Context(), options)
			require.NoError(t, err, "retrying the exact recorded spelling must recover personal content")
			current, err := os.ReadFile(options.Profile)
			require.NoError(t, err)
			require.True(t, bytes.HasPrefix(current, personal))
			require.Contains(t, string(current), pickerBlockBegin)
		})
	}
}

func TestPickerProfileRecoveryCompletedOtherProfileDoesNotBlock(t *testing.T) {
	options := pickerInstallFixture(t, CompletionStyleBash)
	personal := []byte("# existing personal profile\n")
	require.NoError(t, os.WriteFile(options.Profile, personal, 0600))
	_, err := InstallPicker(t.Context(), options)
	require.NoError(t, err)
	require.Len(t, pickerRecoveryArchives(t, options.Profile), 1)
	other := options
	other.Profile += "-unrelated"
	_, err = InstallPicker(t.Context(), other)
	require.NoError(t, err, "completed recovery archives must not block another missing profile")
	current, err := os.ReadFile(options.Profile)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(current, personal))
	active, err := IsPickerInstalled(t.Context(), other)
	require.NoError(t, err)
	require.True(t, active)
}

func TestPickerProfileRecoveryMultiplePendingProfilesRecoverIndependently(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprintf("reverse=%t", reverse), func(t *testing.T) {
			first := pickerInstallFixture(t, CompletionStyleBash)
			first.Profile = filepath.Join(filepath.Dir(first.Profile), "first-profile")
			second := first
			second.Profile = filepath.Join(filepath.Dir(first.Profile), "second-profile")
			profiles := []PickerInstallation{first, second}
			for _, options := range profiles {
				personal := []byte("# personal content for " + filepath.Base(options.Profile) + "\n")
				require.NoError(t, os.WriteFile(options.Profile, personal, 0600))
			}
			for _, options := range profiles {
				pickerRecoveryInterruptAfterCapture(t, options)
			}
			if reverse {
				profiles[0], profiles[1] = profiles[1], profiles[0]
			}
			for _, options := range profiles {
				_, err := InstallPicker(t.Context(), options)
				require.NoError(t, err, "an exact pending spelling must remain recoverable while another profile is pending")
				current, err := os.ReadFile(options.Profile)
				require.NoError(t, err)
				require.True(t, bytes.HasPrefix(current, []byte("# personal content for "+filepath.Base(options.Profile)+"\n")))
				active, err := IsPickerInstalled(t.Context(), options)
				require.NoError(t, err)
				require.True(t, active)
			}
		})
	}
}

func TestPickerProfileRecoveryMissingManifestPreservesContents(t *testing.T) {
	for _, contents := range []string{"empty", "personal next file"} {
		t.Run(contents, func(t *testing.T) {
			options := pickerInstallFixture(t, CompletionStyleBash)
			personal := []byte("# personal startup\n")
			require.NoError(t, os.WriteFile(options.Profile, personal, 0600))
			root, err := os.OpenRoot(filepath.Dir(options.Profile))
			require.NoError(t, err)
			defer root.Close()
			directory := pickerRecoveryPrefix(filepath.Base(options.Profile)) + "interrupted"
			require.NoError(t, createPickerRecoveryDirectory(root, directory))
			path := filepath.Join(filepath.Dir(options.Profile), directory)
			if contents != "empty" {
				require.NoError(t, os.WriteFile(filepath.Join(path, "next"), []byte("# unproven personal bytes\n"), 0600))
			}
			_, err = InstallPicker(t.Context(), options)
			if contents == "empty" {
				require.NoError(t, err)
				_, err = os.Lstat(path)
				require.ErrorIs(t, err, os.ErrNotExist, "an empty pre-manifest directory may be discarded")
				active, err := IsPickerInstalled(t.Context(), options)
				require.NoError(t, err)
				require.True(t, active)
			} else {
				require.Error(t, err, "a missing manifest cannot prove ownership of recovery contents")
				preserved, err := os.ReadFile(filepath.Join(path, "next"))
				require.NoError(t, err)
				require.Equal(t, "# unproven personal bytes\n", string(preserved))
				current, err := os.ReadFile(options.Profile)
				require.NoError(t, err)
				require.Equal(t, personal, current)
			}
		})
	}
}

func pickerRecoveryInterruptAfterCapture(t *testing.T, options PickerInstallation) {
	t.Helper()
	request, err := json.Marshal(map[string]any{"Options": options, "Action": "replace"})
	require.NoError(t, err)
	deadline, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(deadline, os.Args[0], "-test.run=^TestPickerProfileRecoveryInterruptedCapture$")
	child.Env = append(os.Environ(), "OPENAI_CLI_TEST_PICKER_RECOVERY_CRASH="+string(request))
	output, err := child.CombinedOutput()
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit, "%s", output)
	require.Equal(t, 77, exit.ExitCode(), "%s", output)
	_, err = os.Lstat(options.Profile)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestPickerProfileRecoveryPrivateManifest(t *testing.T) {
	options := pickerInstallFixture(t, CompletionStyleZsh)
	require.NoError(t, os.WriteFile(options.Profile, []byte("# private synthetic setting\n"), 0640))
	_, err := InstallPicker(t.Context(), options)
	require.NoError(t, err)
	archives := pickerRecoveryArchives(t, options.Profile)
	require.Len(t, archives, 1)
	entries, err := os.ReadDir(archives[0])
	require.NoError(t, err)
	for _, entry := range entries {
		info, err := os.Lstat(filepath.Join(archives[0], entry.Name()))
		require.NoError(t, err)
		require.True(t, info.Mode().IsRegular())
		if runtime.GOOS != "windows" && entry.Name() != "original" {
			require.Zero(t, info.Mode().Perm()&0077, "new recovery bookkeeping must be private")
		}
	}
	manifest, err := os.ReadFile(filepath.Join(archives[0], "manifest.json"))
	require.NoError(t, err)
	var metadata map[string]any
	require.NoError(t, json.Unmarshal(manifest, &metadata))
	require.Equal(t, map[string]any{"version": float64(1), "profile": filepath.Base(options.Profile)}, metadata)
	require.NotContains(t, string(manifest), filepath.Dir(options.Profile))
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(archives[0], "original"))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0640), info.Mode().Perm(), "retain the original file's metadata inside its private directory")
	}
}

func TestPickerProfileRecoveryCapacityDoesNotGrowOnNoOp(t *testing.T) {
	options := pickerInstallFixture(t, CompletionStyleBash)
	require.NoError(t, os.WriteFile(options.Profile, []byte("# personal startup\n"), 0600))
	for round := range 16 {
		installed, err := InstallPicker(t.Context(), options)
		require.NoError(t, err)
		require.True(t, installed.Changed)
		require.Len(t, pickerRecoveryArchives(t, options.Profile), 2*round+1)
		repeated, err := InstallPicker(t.Context(), options)
		require.NoError(t, err)
		require.False(t, repeated.Changed)
		require.Len(t, pickerRecoveryArchives(t, options.Profile), 2*round+1)
		removed, err := RemovePicker(t.Context(), options)
		require.NoError(t, err)
		require.True(t, removed.Changed)
		repeated, err = RemovePicker(t.Context(), options)
		require.NoError(t, err)
		require.False(t, repeated.Changed)
		require.Len(t, pickerRecoveryArchives(t, options.Profile), 2*round+2)
	}
	before := pickerConfigTree(t, filepath.Dir(options.Profile))
	_, err := InstallPicker(t.Context(), options)
	require.Error(t, err, "a 33rd changing operation must stop before capturing another original")
	after := pickerConfigTree(t, filepath.Dir(options.Profile))
	for _, tree := range []map[string]string{before, after} {
		for path, value := range tree {
			if strings.HasPrefix(value, "d") {
				// Creating and then cleaning a staging file may touch its
				// directory timestamp without leaving any additional state.
				tree[path] = strings.SplitN(value, " ", 2)[0]
			}
		}
	}
	require.Equal(t, before, after, "capacity refusal must leave no additional files")
}

func pickerRecoveryArchives(t *testing.T, profile string) []string {
	t.Helper()
	hash := sha256.Sum256([]byte(filepath.Base(profile)))
	prefix := fmt.Sprintf(".openai-picker-recovery-%x-", hash[:8])
	entries, err := os.ReadDir(filepath.Dir(profile))
	require.NoError(t, err)
	var archives []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			require.True(t, entry.IsDir())
			archives = append(archives, filepath.Join(filepath.Dir(profile), entry.Name()))
		}
	}
	return archives
}

func pickerRecoveryFixture(t *testing.T, action string) (PickerInstallation, os.FileInfo, []byte) {
	t.Helper()
	options := pickerInstallFixture(t, CompletionStyleBash)
	if action == "replace" {
		require.NoError(t, os.WriteFile(options.Profile, []byte("# personal startup\n"), 0600))
	} else {
		_, err := InstallPicker(t.Context(), options)
		require.NoError(t, err)
	}
	info, err := pickerRecoveryFileIdentity(options.Profile)
	require.NoError(t, err)
	data, err := os.ReadFile(options.Profile)
	require.NoError(t, err)
	return options, info, data
}

func pickerRecoveryChange(ctx context.Context, options PickerInstallation, action string) (PickerInstallResult, error) {
	if action == "remove" {
		return RemovePicker(ctx, options)
	}
	return InstallPicker(ctx, options)
}

type pickerRecoveryCaptureContext struct {
	context.Context
	profile   string
	original  os.FileInfo
	action    func() error
	fired     bool
	actionErr error
}

type pickerRecoveryBeforeCaptureContext struct {
	context.Context
	profile   string
	replace   bool
	action    func() error
	fired     bool
	actionErr error
}

func (ctx *pickerRecoveryBeforeCaptureContext) Err() error {
	if !ctx.fired {
		hash := sha256.Sum256([]byte(filepath.Base(ctx.profile)))
		prefix := fmt.Sprintf(".openai-picker-recovery-%x-", hash[:8])
		entries, _ := os.ReadDir(filepath.Dir(ctx.profile))
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), prefix) {
				continue
			}
			directory := filepath.Join(filepath.Dir(ctx.profile), entry.Name())
			if _, err := os.Stat(filepath.Join(directory, "manifest.json")); err != nil {
				continue
			}
			if _, err := os.Lstat(filepath.Join(directory, "original")); !errors.Is(err, os.ErrNotExist) {
				continue
			}
			if ctx.replace {
				if _, err := os.Stat(filepath.Join(directory, "next")); err != nil {
					continue
				}
			}
			ctx.fired = true
			ctx.actionErr = ctx.action()
			break
		}
	}
	return errors.Join(ctx.actionErr, ctx.Context.Err())
}

func (ctx *pickerRecoveryCaptureContext) Err() error {
	if !ctx.fired {
		if _, err := os.Lstat(ctx.profile); errors.Is(err, os.ErrNotExist) {
			retained, err := pickerRecoveryOriginalPath(ctx.profile, ctx.original)
			if err == nil && retained != "" {
				ctx.fired = true
				ctx.actionErr = ctx.action()
			}
		}
	}
	return errors.Join(ctx.actionErr, ctx.Context.Err())
}

func pickerRecoveryFindOriginal(t *testing.T, profile string, expected os.FileInfo) string {
	t.Helper()
	found, err := pickerRecoveryOriginalPath(profile, expected)
	require.NoError(t, err)
	return found
}

func pickerRecoveryOriginalPath(profile string, expected os.FileInfo) (string, error) {
	var found string
	err := filepath.WalkDir(filepath.Dir(profile), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == profile || !entry.Type().IsRegular() {
			return nil
		}
		info, err := pickerRecoveryFileIdentity(path)
		if err != nil {
			return err
		}
		if os.SameFile(expected, info) {
			found = path
		}
		return nil
	})
	return found, err
}

func pickerRecoveryFileIdentity(path string) (os.FileInfo, error) {
	// File.Stat materializes the Windows file ID while the path still exists.
	// Path-based Stat defers that lookup, which fails after capture or while an
	// editor retains a writable handle to the captured file.
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	return info, errors.Join(err, file.Close())
}
