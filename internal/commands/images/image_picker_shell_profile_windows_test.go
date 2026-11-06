package images

import (
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImagePickerWindowsShellHomeMatchingNativePaths(t *testing.T) {
	for _, format := range []string{"native", "forward slashes", "upper case", "lower case", "unset"} {
		t.Run(format, func(t *testing.T) {
			home := pickerShellSetupHome(t)
			if format == "forward slashes" {
				t.Setenv("HOME", filepath.ToSlash(home))
			} else if format == "upper case" {
				t.Setenv("HOME", strings.ToUpper(home))
			} else if format == "lower case" {
				t.Setenv("HOME", strings.ToLower(home))
			} else if format == "unset" {
				t.Setenv("HOME", "")
			}
			targets, err := imagePickerShellTarget(t.Context(), "bash", false, "")
			require.NoError(t, err)
			require.Len(t, targets, 2)
			require.Equal(t, filepath.Join(home, ".bashrc"), targets[0].Profile)
			require.Equal(t, filepath.Join(home, ".bash_profile"), targets[1].Profile)
		})
	}
}

func TestImagePickerWindowsShellHomeAutomaticUsesDirectoryIdentity(t *testing.T) {
	home := t.TempDir()
	current := &user.User{Uid: "1000", HomeDir: home}
	for _, path := range []string{strings.ToUpper(home), strings.ToLower(home), filepath.ToSlash(home)} {
		require.NoError(t, imagePickerAutomaticHome(path, current, 1000, false))
	}
	require.Error(t, imagePickerAutomaticHome(t.TempDir(), current, 1000, false))
	require.Error(t, imagePickerAutomaticHome(filepath.Join(home, "absent"), current, 1000, false))
}
