package updatenotice

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArchiveZshProfilePermissions(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("setup requires an ordinary user")
	}
	for _, tc := range []struct {
		name, target, rights string
		allowed              bool
	}{
		{"shared file", ".zlogin", "write", false},
		{"shared directory", ".", "add_file,delete_child", false},
		{"readable file", ".zlogin", "read", true},
		{"searchable directory", ".", "search,readattr", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			const original = "# personal settings\n"
			require.NoError(t, os.WriteFile(".zlogin", []byte(original), 0600))
			parent, err := os.Stat(".")
			require.NoError(t, err)
			file, err := os.Stat(".zlogin")
			require.NoError(t, err)
			identity := profileID(parent) + "/" + profileID(file)
			out, err := exec.Command("/bin/chmod", "+a", "group:everyone allow "+tc.rights, tc.target).CombinedOutput()
			require.NoError(t, err, string(out))
			require.Equal(t, tc.allowed, enableZshPath(dir, identity, true))
			contents, err := os.ReadFile(".zlogin")
			require.NoError(t, err)
			if tc.allowed {
				require.Contains(t, string(contents), "$HOME/.openai/bin")
			} else {
				require.Equal(t, original, string(contents))
			}
		})
	}
}
