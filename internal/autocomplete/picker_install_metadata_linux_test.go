//go:build linux

package autocomplete

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestPickerInstallPreservesLinuxFileMetadata(t *testing.T) {
	// The named user's access is read-only; the owning group's permission is
	// empty, although stat's group bits show the ACL mask (read). Discarding
	// this ACL while keeping mode 0640 would give the owning group new access.
	acl := binary.LittleEndian.AppendUint32(nil, 2)
	for _, entry := range [][3]uint32{{1, 6, ^uint32(0)}, {2, 4, uint32(os.Getuid()) + 1}, {4, 0, ^uint32(0)}, {16, 4, ^uint32(0)}, {32, 0, ^uint32(0)}} {
		acl = binary.LittleEndian.AppendUint16(acl, uint16(entry[0]))
		acl = binary.LittleEndian.AppendUint16(acl, uint16(entry[1]))
		acl = binary.LittleEndian.AppendUint32(acl, entry[2])
	}
	for _, attribute := range []struct {
		name string
		data []byte
	}{{"user.openai-picker-test", []byte("keep")}, {"system.posix_acl_access", acl}} {
		for _, remove := range []bool{false, true} {
			t.Run(attribute.name+map[bool]string{false: "/install", true: "/remove"}[remove], func(t *testing.T) {
				directory := t.TempDir()
				options := PickerInstallation{Shell: CompletionStyleZsh, Directory: filepath.Join(directory, "scripts"), Profile: filepath.Join(directory, ".zshrc")}
				require.NoError(t, os.WriteFile(options.Profile, []byte("# personal startup\n"), 0640))
				if remove {
					_, err := InstallPicker(t.Context(), options)
					require.NoError(t, err)
				}
				require.NoError(t, unix.Setxattr(options.Profile, attribute.name, attribute.data, 0))
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
				data := make([]byte, len(attribute.data))
				count, err := unix.Getxattr(options.Profile, attribute.name, data)
				require.NoError(t, err)
				require.Equal(t, attribute.data, data[:count])
			})
		}
	}
}

func TestPickerLinuxMetadataAddedAfterSnapshotIsKept(t *testing.T) {
	directory := t.TempDir()
	profile := filepath.Join(directory, ".zshrc")
	require.NoError(t, os.WriteFile(profile, []byte("# personal startup\n"), 0600))
	root, err := os.OpenRoot(directory)
	require.NoError(t, err)
	defer root.Close()
	previous, err := readPickerFile(t.Context(), root, ".zshrc")
	require.NoError(t, err)
	require.NoError(t, unix.Setxattr(profile, "user.openai-picker-test", []byte("keep"), 0))
	require.Error(t, replacePickerProfile(t.Context(), root, ".zshrc", previous, []byte("replacement\n")))
	current, err := os.ReadFile(profile)
	require.NoError(t, err)
	require.Equal(t, previous.data, current)
}

func TestPickerInstallRejectsWritableLinuxAncestorACL(t *testing.T) {
	options := pickerInstallFixture(t, CompletionStyleBash)
	config := filepath.Join(filepath.Dir(options.Profile), "config")
	options.Directory = filepath.Join(config, "openai", "shell")
	require.NoError(t, os.Mkdir(config, 0700))
	acl := binary.LittleEndian.AppendUint32(nil, 2)
	for _, entry := range [][3]uint32{{1, 7, ^uint32(0)}, {2, 7, uint32(os.Getuid()) + 1}, {4, 0, ^uint32(0)}, {16, 7, ^uint32(0)}, {32, 0, ^uint32(0)}} {
		acl = binary.LittleEndian.AppendUint16(acl, uint16(entry[0]))
		acl = binary.LittleEndian.AppendUint16(acl, uint16(entry[1]))
		acl = binary.LittleEndian.AppendUint32(acl, entry[2])
	}
	require.NoError(t, unix.Setxattr(config, "system.posix_acl_access", acl, 0))
	_, err := InstallPicker(t.Context(), options)
	require.ErrorContains(t, err, "protected directory ancestors")
	_, err = os.Lstat(filepath.Join(config, "openai"))
	require.ErrorIs(t, err, os.ErrNotExist, "ACL permissions must be checked before creating descendants")
	_, err = os.Lstat(options.Profile)
	require.ErrorIs(t, err, os.ErrNotExist)
}
