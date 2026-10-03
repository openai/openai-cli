package custom

import (
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImagePickerShellTargetBashLoginPrecedence(t *testing.T) {
	for _, existing := range [][]string{nil, {".profile"}, {".profile", ".bash_login"}, {".profile", ".bash_login", ".bash_profile"}} {
		t.Run(strings.Join(existing, "+"), func(t *testing.T) {
			home := t.TempDir()
			for _, name := range existing {
				require.NoError(t, os.WriteFile(filepath.Join(home, name), []byte("# existing startup\n"), 0600))
			}
			profiles, err := imagePickerSelectShellTargets("bash", "", imagePickerShellPaths{home: home, config: filepath.Join(home, "config")})
			require.NoError(t, err)
			require.Len(t, profiles, 2)
			require.Equal(t, filepath.Join(home, ".bashrc"), profiles[0].Profile)
			login := ".bash_profile"
			if len(existing) != 0 {
				login = existing[len(existing)-1]
			}
			require.Equal(t, filepath.Join(home, login), profiles[1].Profile)
			_, err = os.Lstat(filepath.Join(home, ".bashrc"))
			require.True(t, errors.Is(err, os.ErrNotExist), "selection must not create a profile")
		})
	}
}

func TestImagePickerShellTargetRespectsConfiguredDirectories(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, "config space")
	zsh := filepath.Join(home, "zsh space")
	paths := imagePickerShellPaths{home: home, config: config, zdotdir: zsh}
	for _, tc := range []struct{ shell, profile string }{
		{"zsh", filepath.Join(zsh, ".zshrc")},
		{"fish", filepath.Join(config, "fish", "conf.d", "openai-picker.fish")},
	} {
		t.Run(tc.shell, func(t *testing.T) {
			targets, err := imagePickerSelectShellTargets(tc.shell, "", paths)
			require.NoError(t, err)
			require.Len(t, targets, 1)
			require.Equal(t, tc.profile, targets[0].Profile)
			require.Equal(t, filepath.Join(config, "openai", "shell"), targets[0].Directory)
		})
	}
	_, err := os.Lstat(config)
	require.True(t, errors.Is(err, os.ErrNotExist), "selection must not create configuration directories")
}

func TestImagePickerShellTargetRejectsAmbiguousPaths(t *testing.T) {
	home := t.TempDir()
	for _, paths := range []imagePickerShellPaths{
		{home: "relative", config: home},
		{home: home, config: "relative"},
		{home: home, config: home, zdotdir: "relative"},
		{home: home, config: home, zdotdir: home + "\nother"},
	} {
		_, err := imagePickerSelectShellTargets("zsh", "", paths)
		require.Error(t, err)
	}
	_, err := imagePickerSelectShellTargets("pwsh", "", imagePickerShellPaths{home: home, config: home})
	require.ErrorContains(t, err, "unsupported picker shell")
	_, err = imagePickerSelectShellTargets("zsh", home+"\nother", imagePickerShellPaths{home: home, config: home})
	require.Error(t, err)
}

func TestImagePickerShellTargetExplicitProfileOverridesDiscovery(t *testing.T) {
	home := t.TempDir()
	paths := imagePickerShellPaths{home: home, config: home, zdotdir: "relative"}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		targets, err := imagePickerSelectShellTargets(shell, "explicit-profile", paths)
		require.NoError(t, err)
		require.Len(t, targets, 1)
		want, err := filepath.Abs("explicit-profile")
		require.NoError(t, err)
		require.Equal(t, want, targets[0].Profile)
	}
}

func TestImagePickerAutomaticHomeProtectsAccountBoundary(t *testing.T) {
	home := t.TempDir()
	current := &user.User{Uid: "501", HomeDir: home}
	require.NoError(t, imagePickerAutomaticHome(home, current, 501, false))
	for _, tc := range []struct {
		name string
		home string
		user *user.User
		euid int
		sudo bool
	}{
		{"root effective UID", home, current, 0, false},
		{"root user", home, &user.User{Uid: "0", HomeDir: home}, 501, false},
		{"sudo", home, current, 501, true},
		{"other home", filepath.Join(home, "other"), current, 501, false},
		{"relative home", "relative", current, 501, false},
		{"unknown account home", home, &user.User{Uid: "501"}, 501, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, imagePickerAutomaticHome(tc.home, tc.user, tc.euid, tc.sudo))
		})
	}
}

func TestImagePickerShellTargetAutomaticRejectsTemporaryHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("ZDOTDIR", home)
	_, err := imagePickerShellTarget(context.Background(), "", true, "")
	require.Error(t, err)
	entries, err := os.ReadDir(home)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestImagePickerShellTargetCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := imagePickerShellTarget(ctx, "zsh", false, "")
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, imagePickerParentShell(ctx))
}
