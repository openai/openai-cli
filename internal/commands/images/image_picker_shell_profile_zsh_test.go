package images

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImagePickerShellTargetNativeZshStartupEnvironment(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("native zsh is unavailable")
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	for _, test := range []struct {
		name, startup string
		allowed       bool
		override      bool
	}{
		{"unexported custom", `unset ZDOTDIR; ZDOTDIR=$OPENAI_TEST_ZSH_DIRECTORY`, false, true},
		{"unset", `unset ZDOTDIR`, false, true},
		{"unexported empty", `unset ZDOTDIR; ZDOTDIR=; unsetopt RCS`, false, false},
		{"exported empty", `export ZDOTDIR=; unsetopt RCS`, false, false},
		{"exported relative", `export ZDOTDIR=relative`, false, true},
		{"exported absolute", `export ZDOTDIR=$OPENAI_TEST_ZSH_DIRECTORY`, true, false},
	} {
		modes := []string{"default"}
		if !test.allowed {
			modes = append(modes, "automatic")
		}
		if test.override {
			modes = append(modes, "override")
		}
		for _, mode := range modes {
			t.Run(test.name+"/"+mode, func(t *testing.T) {
				home := t.TempDir()
				custom := filepath.Join(home, "custom startup")
				relative := filepath.Join(home, "relative")
				originals := map[string][]byte{}
				for _, directory := range []string{home, custom, relative} {
					require.NoError(t, os.MkdirAll(directory, 0700))
					path := filepath.Join(directory, ".zshrc")
					originals[path] = []byte("# synthetic existing startup\n")
					require.NoError(t, os.WriteFile(path, originals[path], 0600))
				}
				require.NoError(t, os.WriteFile(filepath.Join(home, ".zshenv"), []byte(test.startup+"\n"), 0600))

				// The parent shell resolves this expression before launching the
				// child. '-' deliberately preserves an explicitly empty value.
				script := `export OPENAI_TEST_ZSH_PROFILE="${ZDOTDIR-$HOME}/.zshrc"
"$OPENAI_TEST_BINARY" -test.run '^TestImagePickerZshStartupChild$'
picker_test_status=$?
exit "$picker_test_status"`
				command := exec.CommandContext(t.Context(), zsh, "-d", "-i", "-c", script)
				command.Dir = home
				command.Env = []string{
					"PATH=" + os.Getenv("PATH"), "SHELL=" + zsh, "HOME=" + home, "USERPROFILE=" + home,
					"XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "APPDATA=" + filepath.Join(home, "config"),
					"OPENAI_TEST_ZSH_CHILD=1", "OPENAI_TEST_BINARY=" + binary, "OPENAI_TEST_ZSH_DIRECTORY=" + custom,
					"OPENAI_TEST_ZSH_MODE=" + mode,
				}
				if test.allowed {
					command.Env = append(command.Env, "OPENAI_TEST_ZSH_DEFAULT=allowed")
				}
				output, err := command.CombinedOutput()
				require.NoError(t, err, string(output))
				for path, before := range originals {
					after, err := os.ReadFile(path)
					require.NoError(t, err)
					require.Equal(t, before, after, "must preserve the selected and unrelated startup files")
				}
				if !test.allowed && mode != "override" {
					_, err := os.Stat(filepath.Join(home, "config"))
					require.ErrorIs(t, err, os.ErrNotExist, "ambiguous setup must not create scripts, locks, or preferences")
				}
			})
		}
	}
}
