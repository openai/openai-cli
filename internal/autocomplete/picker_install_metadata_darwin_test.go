//go:build darwin

package autocomplete

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestPickerInstallPreservesDarwinFileMetadata(t *testing.T) {
	for _, kind := range []string{"extended attribute", "ACL", "BSD flag"} {
		for _, remove := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/install", true: "/remove"}[remove], func(t *testing.T) {
				directory := t.TempDir()
				options := PickerInstallation{Shell: CompletionStyleZsh, Directory: filepath.Join(directory, "scripts"), Profile: filepath.Join(directory, ".zshrc")}
				require.NoError(t, os.WriteFile(options.Profile, []byte("# personal startup\n"), 0640))
				if remove {
					_, err := InstallPicker(t.Context(), options)
					require.NoError(t, err)
				}
				switch kind {
				case "extended attribute":
					require.NoError(t, unix.Setxattr(options.Profile, "com.example.openai-picker-test", []byte("keep"), 0))
				case "ACL":
					output, err := exec.Command("/bin/chmod", "+a", "everyone deny write", options.Profile).CombinedOutput()
					require.NoError(t, err, "%s", output)
				case "BSD flag":
					require.NoError(t, unix.Chflags(options.Profile, unix.UF_HIDDEN))
				}
				before, err := os.ReadFile(options.Profile)
				require.NoError(t, err)
				identity, err := os.Stat(options.Profile)
				require.NoError(t, err)
				if remove {
					_, err = RemovePicker(t.Context(), options)
				} else {
					_, err = InstallPicker(t.Context(), options)
				}
				require.Error(t, err, "setup must not replace protected metadata")
				after, err := os.ReadFile(options.Profile)
				require.NoError(t, err)
				require.Equal(t, before, after)
				current, err := os.Stat(options.Profile)
				require.NoError(t, err)
				require.True(t, os.SameFile(identity, current))
				require.Equal(t, identity.Mode(), current.Mode())
				switch kind {
				case "extended attribute":
					var data [4]byte
					count, err := unix.Getxattr(options.Profile, "com.example.openai-picker-test", data[:])
					require.NoError(t, err)
					require.Equal(t, "keep", string(data[:count]))
				case "ACL":
					output, err := exec.Command("/bin/ls", "-le", options.Profile).CombinedOutput()
					require.NoError(t, err)
					require.Contains(t, string(output), "everyone deny write")
				case "BSD flag":
					var info unix.Stat_t
					require.NoError(t, unix.Stat(options.Profile, &info))
					require.Equal(t, uint32(unix.UF_HIDDEN), info.Flags)
				}
			})
		}
	}
}

func TestPickerDarwinMetadataAddedAfterSnapshotIsKept(t *testing.T) {
	directory := t.TempDir()
	profile := filepath.Join(directory, ".zshrc")
	require.NoError(t, os.WriteFile(profile, []byte("# personal startup\n"), 0600))
	root, err := os.OpenRoot(directory)
	require.NoError(t, err)
	defer root.Close()
	previous, err := readPickerFile(t.Context(), root, ".zshrc")
	require.NoError(t, err)
	require.NoError(t, unix.Setxattr(profile, "com.example.openai-picker-test", []byte("keep"), 0))
	require.Error(t, replacePickerProfile(t.Context(), root, ".zshrc", previous, []byte("replacement\n")))
	current, err := os.ReadFile(profile)
	require.NoError(t, err)
	require.Equal(t, previous.data, current)
}

func TestPickerInstallRejectsInheritedScriptACL(t *testing.T) {
	options := pickerInstallFixture(t, CompletionStyleZsh)
	original := []byte("# personal startup\n")
	require.NoError(t, os.WriteFile(options.Profile, original, 0600))
	before, err := os.Stat(options.Profile)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(options.Directory, 0700))
	output, err := exec.Command("/bin/chmod", "+a", "everyone allow write,append,file_inherit", options.Directory).CombinedOutput()
	require.NoError(t, err, "%s", output)
	// The directory's POSIX mode remains private, but a newly created script
	// inherits a writable ACL. It must never become the profile's source.
	_, err = InstallPicker(t.Context(), options)
	require.ErrorContains(t, err, "protected or unreadable access permissions")
	after, err := os.Stat(options.Profile)
	require.NoError(t, err)
	require.True(t, os.SameFile(before, after))
	data, err := os.ReadFile(options.Profile)
	require.NoError(t, err)
	require.Equal(t, original, data)
	files, err := os.ReadDir(options.Directory)
	require.NoError(t, err)
	for _, file := range files {
		require.Equal(t, ".picker-install.lock", file.Name(), "unsafe script or staging file was left behind")
	}
}
