//go:build linux

package autocomplete

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestPickerRenameCapabilityClassification(t *testing.T) {
	for _, tc := range []struct {
		name        string
		err         error
		unsupported bool
	}{
		{"success", nil, false},
		{"ENOSYS", unix.ENOSYS, true},
		{"EOPNOTSUPP", unix.EOPNOTSUPP, true},
		{"EINVAL", unix.EINVAL, true},
		{"EACCES", unix.EACCES, false},
		{"EPERM", unix.EPERM, false},
		{"EROFS", unix.EROFS, false},
		{"EEXIST", unix.EEXIST, false},
		{"ENOENT", unix.ENOENT, false},
		{"EBADF", unix.EBADF, false},
		{"EXDEV", unix.EXDEV, false},
		{"canceled", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
		{"wrapped unrelated operation", &os.PathError{Op: "open", Path: "synthetic-private", Err: unix.ENOSYS}, false},
		{"wrapped unrelated EINVAL", &os.PathError{Op: "open", Path: "synthetic-private", Err: unix.EINVAL}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := pickerRenameCapabilityError(tc.err)
			require.Equal(t, tc.unsupported, errors.Is(got, ErrPickerAtomicRenameUnsupported))
			if tc.err != nil {
				require.ErrorIs(t, got, tc.err)
			}
			if !tc.unsupported {
				require.Equal(t, tc.err, got)
			}
		})
	}
	for _, errno := range []error{unix.ENOSYS, unix.EOPNOTSUPP, unix.EINVAL} {
		joined := errors.Join(errPickerProfilePublished, pickerRenameCapabilityError(errno), context.Canceled)
		require.ErrorIs(t, joined, errPickerProfilePublished)
		require.ErrorIs(t, joined, context.Canceled)
		require.ErrorIs(t, joined, errno)
		require.ErrorIs(t, joined, ErrPickerAtomicRenameUnsupported)
	}
}

// The lane selects its expected contract explicitly. A refusal never turns a
// positive installation assertion into a pass, and unrelated failures fail both.
func TestPickerNativeSetupContract(t *testing.T) {
	contract := os.Getenv("OPENAI_CLI_PICKER_SETUP_CONTRACT")
	if contract == "" {
		contract = "supported"
	}
	require.Contains(t, []string{"supported", "refused"}, contract)
	errno := ""
	checks := 0
	run := func(name string, check func(*testing.T)) {
		t.Run(name, func(t *testing.T) {
			check(t)
			checks++
		})
	}
	checkOperation := func(t *testing.T, err error) {
		t.Helper()
		if contract == "supported" {
			require.NoError(t, err)
			return
		}
		require.ErrorIs(t, err, ErrPickerAtomicRenameUnsupported)
		observed := ""
		switch {
		case errors.Is(err, unix.ENOSYS):
			observed = "ENOSYS"
		case errors.Is(err, unix.EOPNOTSUPP):
			observed = "EOPNOTSUPP"
		case errors.Is(err, unix.EINVAL):
			observed = "EINVAL"
		default:
			t.Fatalf("capability refusal lost its operation errno: %v", err)
		}
		if errno == "" {
			errno = observed
		}
		require.Equal(t, errno, observed, "the selected native fixture must have a consistent operation contract")
	}
	run("rename_absent", func(t *testing.T) {
		root, err := os.OpenRoot(t.TempDir())
		require.NoError(t, err)
		defer root.Close()
		require.NoError(t, root.WriteFile("source", []byte("retained original\n"), 0600))
		before, err := root.Stat("source")
		require.NoError(t, err)
		checkOperation(t, renamePickerFileNoReplace(root, "source", "destination"))
		kept, absent := "destination", "source"
		if contract == "refused" {
			kept, absent = absent, kept
		}
		after, err := root.Stat(kept)
		require.NoError(t, err)
		require.True(t, os.SameFile(before, after))
		data, err := root.ReadFile(kept)
		require.NoError(t, err)
		require.Equal(t, "retained original\n", string(data))
		_, err = root.Lstat(absent)
		require.ErrorIs(t, err, os.ErrNotExist)
	})
	run("rename_existing", func(t *testing.T) {
		directory := t.TempDir()
		root, err := os.OpenRoot(directory)
		require.NoError(t, err)
		defer root.Close()
		for _, name := range []string{"source", "destination"} {
			require.NoError(t, root.WriteFile(name, []byte(name), 0600))
		}
		before := nativePickerSnapshot(t, directory)
		err = renamePickerFileNoReplace(root, "source", "destination")
		if contract == "supported" {
			require.ErrorIs(t, err, os.ErrExist)
			require.NotErrorIs(t, err, ErrPickerAtomicRenameUnsupported)
		} else {
			checkOperation(t, err)
		}
		requireNativePickerSnapshot(t, before)
	})
	for _, existing := range []bool{false, true} {
		name := "install_absent"
		if existing {
			name = "install_existing"
		}
		run(name, func(t *testing.T) {
			options := pickerInstallFixture(t, CompletionStyleBash)
			if existing {
				require.NoError(t, os.WriteFile(options.Profile, []byte("# original\n"), 0600))
			}
			before := nativePickerSnapshot(t, filepath.Dir(options.Profile))
			result, err := InstallPicker(t.Context(), options)
			checkOperation(t, err)
			if contract == "refused" {
				require.False(t, result.Changed)
				requireNativePickerSnapshot(t, before)
				if !existing {
					_, err := os.Lstat(options.Profile)
					require.ErrorIs(t, err, os.ErrNotExist)
				}
				scripts, err := filepath.Glob(filepath.Join(options.Directory, "picker-*.bash"))
				require.NoError(t, err)
				require.Empty(t, scripts, "refusal must clean the uncommitted script")
				staging, err := filepath.Glob(filepath.Join(filepath.Dir(options.Profile), ".openai-picker-*"))
				require.NoError(t, err)
				require.Empty(t, staging, "refusal before capture must clean temporary profile state")
				return
			}
			require.True(t, result.Changed)
			installed, err := IsPickerInstalled(t.Context(), options)
			require.NoError(t, err)
			require.True(t, installed)
		})
	}
	run("seeded_noop", func(t *testing.T) {
		options := pickerInstallFixture(t, CompletionStyleBash)
		seedNativePickerInstallation(t, options)
		before := nativePickerSnapshot(t, filepath.Dir(options.Profile))
		for range 2 {
			installed, err := IsPickerInstalled(t.Context(), options)
			require.NoError(t, err)
			require.True(t, installed)
			result, err := InstallPicker(t.Context(), options)
			require.NoError(t, err)
			require.False(t, result.Changed)
			requireNativePickerSnapshot(t, before)
		}
	})
	run("seeded_remove", func(t *testing.T) {
		options := pickerInstallFixture(t, CompletionStyleBash)
		script := seedNativePickerInstallation(t, options)
		before := nativePickerSnapshot(t, filepath.Dir(options.Profile))
		result, err := RemovePicker(t.Context(), options)
		checkOperation(t, err)
		if contract == "refused" {
			require.False(t, result.Changed)
			requireNativePickerSnapshot(t, before)
			installed, err := IsPickerInstalled(t.Context(), options)
			require.NoError(t, err)
			require.True(t, installed, "refused removal may leave an existing shortcut active")
			return
		}
		require.True(t, result.Changed)
		data, err := os.ReadFile(options.Profile)
		require.NoError(t, err)
		require.Equal(t, "# seeded existing startup\n", string(data))
		_, err = os.Lstat(script)
		require.ErrorIs(t, err, os.ErrNotExist)
	})
	run("missing_profile_cleanup", func(t *testing.T) {
		options := pickerInstallFixture(t, CompletionStyleBash)
		script := seedNativePickerInstallation(t, options)
		require.NoError(t, os.Remove(options.Profile))
		result, err := RemovePicker(t.Context(), options)
		require.NoError(t, err)
		require.True(t, result.Changed)
		for _, path := range []string{script, options.Profile} {
			_, err := os.Lstat(path)
			require.ErrorIs(t, err, os.ErrNotExist)
		}
		result, err = RemovePicker(t.Context(), options)
		require.NoError(t, err)
		require.False(t, result.Changed)
	})
	for _, replaced := range []bool{false, true} {
		name := "pending_recovery"
		if replaced {
			name = "replaced_profile"
		}
		run(name, func(t *testing.T) {
			options := pickerInstallFixture(t, CompletionStyleBash)
			directory := filepath.Dir(options.Profile)
			recovery := filepath.Join(directory, pickerRecoveryPrefix(filepath.Base(options.Profile))+"fixture")
			require.NoError(t, os.Mkdir(recovery, 0700))
			manifest, err := json.Marshal(pickerRecoveryManifest{Version: 1, Profile: filepath.Base(options.Profile)})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(recovery, "manifest.json"), manifest, 0600))
			require.NoError(t, os.WriteFile(filepath.Join(recovery, "original"), []byte("# recovered original\n"), 0600))
			original, err := os.Stat(filepath.Join(recovery, "original"))
			require.NoError(t, err)
			if replaced {
				require.NoError(t, os.WriteFile(options.Profile, []byte("# later editor save\n"), 0600))
			}
			before := nativePickerSnapshot(t, directory)
			root, err := os.OpenRoot(directory)
			require.NoError(t, err)
			defer root.Close()
			checkOperation(t, recoverPickerProfiles(t.Context(), root, filepath.Base(options.Profile)))
			if contract == "refused" || replaced {
				requireNativePickerSnapshot(t, before)
			} else {
				restored, err := os.Stat(options.Profile)
				require.NoError(t, err)
				require.True(t, os.SameFile(original, restored))
				data, err := os.ReadFile(options.Profile)
				require.NoError(t, err)
				require.Equal(t, "# recovered original\n", string(data))
			}
			if contract == "refused" {
				_, err := os.Lstat(filepath.Join(recovery, "complete"))
				require.ErrorIs(t, err, os.ErrNotExist, "refusal must not mark recovery complete")
				if !replaced {
					_, err := os.Lstat(options.Profile)
					require.ErrorIs(t, err, os.ErrNotExist)
				}
			}
		})
	}
	run("canceled", func(t *testing.T) {
		options := pickerInstallFixture(t, CompletionStyleBash)
		seedNativePickerInstallation(t, options)
		before := nativePickerSnapshot(t, filepath.Dir(options.Profile))
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		for _, operation := range []func(context.Context, PickerInstallation) (PickerInstallResult, error){InstallPicker, RemovePicker} {
			_, err := operation(ctx, options)
			require.ErrorIs(t, err, context.Canceled)
			require.NotErrorIs(t, err, ErrPickerAtomicRenameUnsupported)
		}
		requireNativePickerSnapshot(t, before)
	})
	if !t.Failed() && checks == 10 {
		receipt, err := json.Marshal(map[string]any{"contract": contract, "errno": errno, "checks": checks})
		require.NoError(t, err)
		fmt.Println("PICKER_CAPABILITY " + string(receipt))
	}
}

func seedNativePickerInstallation(t *testing.T, options PickerInstallation) string {
	t.Helper()
	script, err := renderInstalledPicker(options)
	require.NoError(t, err)
	path := filepath.Join(options.Directory, pickerScriptName(options, script))
	require.NoError(t, os.MkdirAll(options.Directory, 0700))
	require.NoError(t, os.WriteFile(path, script, 0600))
	block := renderPickerBlock(pickerInstalledBlock{Shell: options.Shell, Script: path})
	require.NoError(t, os.WriteFile(options.Profile, append([]byte("# seeded existing startup\n"), block...), 0600))
	return path
}

func nativePickerSnapshot(t *testing.T, directory string) map[string]pickerFileSnapshot {
	t.Helper()
	files := make(map[string]pickerFileSnapshot)
	require.NoError(t, filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		files[path] = pickerFileSnapshot{info: info, data: data}
		return err
	}))
	return files
}

func requireNativePickerSnapshot(t *testing.T, before map[string]pickerFileSnapshot) {
	t.Helper()
	for path, snapshot := range before {
		info, err := os.Lstat(path)
		require.NoError(t, err)
		require.True(t, os.SameFile(snapshot.info, info), "retained file identity changed: %s", path)
		require.Equal(t, snapshot.info.Mode(), info.Mode())
		require.Equal(t, snapshot.info.ModTime(), info.ModTime())
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, snapshot.data, data)
	}
}

// Export candidate-rendered bytes only. The caller owns its private fixture and
// must attribute it as seeded existing state, never successful installation.
func TestPickerStartupFixtureExport(t *testing.T) {
	var request struct {
		Directory string   `json:"directory"`
		Profiles  []string `json:"profiles"`
	}
	input := os.Getenv("OPENAI_CLI_PICKER_FIXTURE_REQUEST")
	if input == "" {
		request.Directory = "/synthetic-fixture/config/openai/shell"
		request.Profiles = []string{"/synthetic-fixture/.bashrc"}
	} else {
		decoder := json.NewDecoder(strings.NewReader(input))
		decoder.DisallowUnknownFields()
		require.NoError(t, decoder.Decode(&request))
		require.True(t, json.Valid([]byte(input)))
	}
	require.GreaterOrEqual(t, len(request.Profiles), 1)
	require.LessOrEqual(t, len(request.Profiles), 4)
	type fixture struct {
		Profile    string `json:"profile"`
		ScriptName string `json:"script_name"`
		Script     string `json:"script"`
		Block      string `json:"block"`
	}
	fixtures := make([]fixture, 0, len(request.Profiles))
	seen := make(map[string]bool)
	for _, profile := range request.Profiles {
		require.False(t, seen[profile], "duplicate fixture profile")
		seen[profile] = true
		options := PickerInstallation{Shell: CompletionStyleBash, Directory: request.Directory, Profile: profile}
		require.NoError(t, validatePickerInstallation(options))
		script, err := renderInstalledPicker(options)
		require.NoError(t, err)
		name := pickerScriptName(options, script)
		block := renderPickerBlock(pickerInstalledBlock{Shell: options.Shell, Script: filepath.Join(options.Directory, name)})
		_, _, parsed, err := parsePickerBlock(block, options)
		require.NoError(t, err)
		require.NotNil(t, parsed)
		require.True(t, validOwnedPickerScript(options, *parsed, script))
		fixtures = append(fixtures, fixture{profile, name, string(script), string(block)})
	}
	if input != "" {
		data, err := json.Marshal(map[string]any{"contract": "seeded-existing-state", "shell": "bash", "directory": request.Directory, "fixtures": fixtures})
		require.NoError(t, err)
		fmt.Println("PICKER_STARTUP_FIXTURE " + string(data))
	}
}
