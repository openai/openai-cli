//go:build darwin

package autocomplete

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPickerRecoveryDirectoryRejectsDarwinInheritedACL(t *testing.T) {
	directory := t.TempDir()
	output, err := exec.Command("/bin/chmod", "+a", "everyone allow read,list,directory_inherit", directory).CombinedOutput()
	require.NoError(t, err, "%s", output)
	root, err := os.OpenRoot(directory)
	require.NoError(t, err)
	defer root.Close()
	require.Error(t, createPickerRecoveryDirectory(root, "recovery"))
	_, err = root.Lstat("recovery")
	require.ErrorIs(t, err, os.ErrNotExist, "an inherited ACL grant must be rejected before creating recovery state")
}
