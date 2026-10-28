package autocomplete

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPickerInstallConfigDirectoryChanges(t *testing.T) {
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish} {
		for _, action := range []string{"remove", "refresh", "modified script"} {
			t.Run(string(shell)+"/"+action, func(t *testing.T) {
				original := pickerInstallFixture(t, shell)
				personal := []byte("# personal startup\n")
				require.NoError(t, os.WriteFile(original.Profile, personal, 0600))
				installed, err := InstallPicker(t.Context(), original)
				require.NoError(t, err)
				if action == "refresh" {
					data, err := os.ReadFile(installed.ScriptPath)
					require.NoError(t, err)
					older := append(data, []byte("\n# earlier CLI script\n")...)
					oldPath := filepath.Join(original.Directory, pickerScriptName(original, older))
					require.NoError(t, os.WriteFile(oldPath, older, 0600))
					require.NoError(t, os.WriteFile(original.Profile, append(bytes.Clone(personal), renderPickerBlock(pickerInstalledBlock{Shell: shell, Script: oldPath})...), 0600))
					require.NoError(t, os.Remove(installed.ScriptPath))
					installed.ScriptPath = oldPath
				} else if action == "modified script" {
					require.NoError(t, os.WriteFile(installed.ScriptPath, []byte("# user modification\n"), 0600))
				}
				changed := original
				changed.Directory = filepath.Join(filepath.Dir(original.Profile), "new-config", "openai", "shell")
				profileBefore, err := os.ReadFile(original.Profile)
				require.NoError(t, err)
				active, err := IsPickerInstalled(t.Context(), changed)
				if action == "modified script" {
					require.Error(t, err)
					_, err = InstallPicker(t.Context(), changed)
					require.Error(t, err)
					_, err = RemovePicker(t.Context(), changed)
					require.Error(t, err)
					after, err := os.ReadFile(original.Profile)
					require.NoError(t, err)
					require.Equal(t, profileBefore, after)
					data, err := os.ReadFile(installed.ScriptPath)
					require.NoError(t, err)
					require.Equal(t, "# user modification\n", string(data))
					return
				}
				require.NoError(t, err)
				require.Equal(t, action != "refresh", active)
				if action == "refresh" {
					updated, err := InstallPicker(t.Context(), changed)
					require.NoError(t, err)
					require.True(t, updated.Changed)
					require.Equal(t, original.Directory, filepath.Dir(updated.ScriptPath))
					_, err = os.Stat(installed.ScriptPath)
					require.ErrorIs(t, err, os.ErrNotExist)
					installed = updated
					active, err = IsPickerInstalled(t.Context(), changed)
					require.NoError(t, err)
					require.True(t, active)
					identity, err := os.Stat(original.Profile)
					require.NoError(t, err)
					again, err := InstallPicker(t.Context(), changed)
					require.NoError(t, err)
					require.False(t, again.Changed)
					after, err := os.Stat(original.Profile)
					require.NoError(t, err)
					require.True(t, os.SameFile(identity, after))
				}
				removed, err := RemovePicker(t.Context(), changed)
				require.NoError(t, err)
				require.True(t, removed.Changed)
				restored, err := os.ReadFile(original.Profile)
				require.NoError(t, err)
				require.Equal(t, personal, restored)
				_, err = os.Stat(installed.ScriptPath)
				require.ErrorIs(t, err, os.ErrNotExist)
				_, err = os.Stat(changed.Directory)
				require.ErrorIs(t, err, os.ErrNotExist)
			})
		}
	}
}

func TestPickerInstallIgnoresUnusedConfigDirectory(t *testing.T) {
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish} {
		for _, unsafe := range []string{"symlink", "writable", "file ancestry"} {
			for _, action := range []string{"inspect", "refresh", "remove"} {
				t.Run(fmt.Sprintf("%s/%s/%s", shell, unsafe, action), func(t *testing.T) {
					original := pickerInstallFixture(t, shell)
					personal := []byte("# personal startup\n")
					// Keep a real managed profile while selecting an unusable new
					// configuration root that none of these operations should use.
					require.NoError(t, os.WriteFile(original.Profile, personal, 0600))
					installed, err := InstallPicker(t.Context(), original)
					require.NoError(t, err)
					if action == "refresh" {
						data, err := os.ReadFile(installed.ScriptPath)
						require.NoError(t, err)
						older := append(data, []byte("\n# earlier CLI script\n")...)
						oldPath := filepath.Join(original.Directory, pickerScriptName(original, older))
						require.NoError(t, os.WriteFile(oldPath, older, 0600))
						require.NoError(t, os.WriteFile(original.Profile, append(bytes.Clone(personal), renderPickerBlock(pickerInstalledBlock{Shell: shell, Script: oldPath})...), 0600))
						require.NoError(t, os.Remove(installed.ScriptPath))
						installed.ScriptPath = oldPath
					}
					changed := original
					unusedRoot := filepath.Join(filepath.Dir(original.Profile), "unused-config")
					changed.Directory = pickerUnsafeRequestedDirectory(t, unusedRoot, unsafe)
					untouched := pickerConfigTree(t, unusedRoot)
					switch action {
					case "inspect":
						active, err := IsPickerInstalled(t.Context(), changed)
						require.NoError(t, err)
						require.True(t, active)
					case "refresh":
						active, err := IsPickerInstalled(t.Context(), changed)
						require.NoError(t, err)
						require.False(t, active)
						updated, err := InstallPicker(t.Context(), changed)
						require.NoError(t, err)
						require.True(t, updated.Changed)
						require.Equal(t, original.Directory, filepath.Dir(updated.ScriptPath))
						_, err = os.Stat(installed.ScriptPath)
						require.ErrorIs(t, err, os.ErrNotExist)
						active, err = IsPickerInstalled(t.Context(), changed)
						require.NoError(t, err)
						require.True(t, active)
						identity, err := os.Stat(original.Profile)
						require.NoError(t, err)
						again, err := InstallPicker(t.Context(), changed)
						require.NoError(t, err)
						require.False(t, again.Changed)
						after, err := os.Stat(original.Profile)
						require.NoError(t, err)
						require.True(t, os.SameFile(identity, after))
					case "remove":
						removed, err := RemovePicker(t.Context(), changed)
						require.NoError(t, err)
						require.True(t, removed.Changed)
						restored, err := os.ReadFile(original.Profile)
						require.NoError(t, err)
						require.Equal(t, personal, restored)
						_, err = os.Stat(installed.ScriptPath)
						require.ErrorIs(t, err, os.ErrNotExist)
					}
					require.Equal(t, untouched, pickerConfigTree(t, unusedRoot), "the unselected configuration tree must remain untouched")
				})
			}
		}
	}
}

func TestPickerInstallConfigDirectoryRefusalPreservesFiles(t *testing.T) {
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish} {
		for _, unsafe := range []string{"symlink", "writable", "file ancestry"} {
			for _, state := range []string{"absent profile parent", "unmanaged profile", "unsafe recorded directory"} {
				t.Run(fmt.Sprintf("%s/%s/%s", shell, unsafe, state), func(t *testing.T) {
					options := pickerInstallFixture(t, shell)
					home := filepath.Dir(options.Profile)
					if state == "unsafe recorded directory" {
						_, err := InstallPicker(t.Context(), options)
						require.NoError(t, err)
						parent := filepath.Dir(options.Directory)
						switch unsafe {
						case "symlink":
							target := options.Directory + "-saved"
							require.NoError(t, os.Rename(options.Directory, target))
							pickerConfigSymlink(t, target, options.Directory)
						case "writable":
							pickerConfigWritable(t, parent)
						case "file ancestry":
							require.NoError(t, os.Rename(parent, parent+"-saved"))
							require.NoError(t, os.WriteFile(parent, []byte("personal file\n"), 0600))
						}
						options.Directory = filepath.Join(home, "new-config", "openai", "shell")
					} else {
						options.Directory = pickerUnsafeRequestedDirectory(t, filepath.Join(home, "unsafe-config"), unsafe)
						if state == "unmanaged profile" {
							require.NoError(t, os.WriteFile(options.Profile, []byte("# personal startup\n"), 0600))
						} else {
							options.Profile = filepath.Join(home, "absent", "profile")
						}
					}
					before := pickerConfigTree(t, home)
					active, err := IsPickerInstalled(t.Context(), options)
					require.Error(t, err)
					require.False(t, active)
					_, err = InstallPicker(t.Context(), options)
					require.Error(t, err)
					_, err = RemovePicker(t.Context(), options)
					require.Error(t, err)
					require.Equal(t, before, pickerConfigTree(t, home), "refused operations must not create locks, directories, or rewrite files")
				})
			}
		}
	}
}

func pickerUnsafeRequestedDirectory(t *testing.T, root, unsafe string) string {
	t.Helper()
	require.NoError(t, os.Mkdir(root, 0700))
	config := filepath.Join(root, "config")
	if unsafe == "file ancestry" {
		require.NoError(t, os.WriteFile(config, []byte("personal file\n"), 0600))
	} else {
		require.NoError(t, os.Mkdir(config, 0700))
		if unsafe == "writable" {
			pickerConfigWritable(t, config)
		} else {
			target := filepath.Join(root, "target")
			require.NoError(t, os.Mkdir(target, 0700))
			require.NoError(t, os.WriteFile(filepath.Join(target, "personal"), []byte("untouched\n"), 0600))
			pickerConfigSymlink(t, target, filepath.Join(config, "openai"))
		}
	}
	return filepath.Join(config, "openai", "shell")
}

func pickerConfigSymlink(t *testing.T, target, link string) {
	t.Helper()
	err := os.Symlink(target, link)
	if err != nil && runtime.GOOS == "windows" {
		t.Skipf("Windows symlink creation unavailable: %v", err)
	}
	require.NoError(t, err)
}

func pickerConfigWritable(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix directory permissions; Windows DACL coverage is separate")
	}
	require.NoError(t, os.Chmod(path, 0777))
}

func pickerConfigTree(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Windows directory enumeration can retain metadata from before a
		// child's creation. Read the path itself for the current snapshot.
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		value := fmt.Sprintf("%v %d", info.Mode(), info.ModTime().UnixNano())
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += " " + string(data)
		} else if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			value += " " + target
		}
		tree[path] = value
		return nil
	}))
	return tree
}
