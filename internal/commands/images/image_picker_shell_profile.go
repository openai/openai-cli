package images

import (
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"runtime"

	"github.com/openai/openai-cli/internal/autocomplete"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
)

func imagePickerShellTarget(ctx context.Context, shell string, automatic bool, profileOverride string) ([]autocomplete.PickerInstallation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if automatic {
		if shell == "" {
			// Installer setup uses the preferred login shell, never executing SHELL.
			if preferred := os.Getenv("SHELL"); imagePickerAbsolutePath(preferred) {
				shell = cliterminal.ShellName(preferred)
			}
			if shell == "" && cliterminal.ParentShell(ctx) == "pwsh" {
				return nil, nil
			}
		}
		if shell == "pwsh" {
			return nil, nil
		}
		// An absent environment value cannot distinguish unset ZDOTDIR from
		// a parent-shell parameter that was never exported. Empty and relative
		// values also cannot safely select an absolute startup file.
		if shell == "zsh" && !imagePickerAbsolutePath(os.Getenv("ZDOTDIR")) {
			return nil, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || !imagePickerAbsolutePath(home) {
		return nil, errors.New("cannot determine an absolute home directory for shell setup")
	}
	if runtime.GOOS == "windows" && profileOverride == "" {
		// UserHomeDir uses USERPROFILE on Windows, while Unix shells read
		// HOME. Do not guess a native equivalent for MSYS or other shell paths.
		if shellHome := os.Getenv("HOME"); shellHome != "" && (!imagePickerAbsolutePath(shellHome) || !imagePickerSameHome(shellHome, home)) {
			if automatic {
				return nil, nil
			}
			return nil, errors.New("Windows shell HOME differs from USERPROFILE; specify the shell startup file with --profile")
		}
	}
	if automatic {
		current, currentErr := user.Current()
		if currentErr != nil {
			return nil, errors.New("cannot identify the current user for automatic shell setup")
		}
		sudo := os.Getenv("SUDO_USER") != "" || os.Getenv("SUDO_UID") != "" || os.Getenv("SUDO_GID") != ""
		if err := imagePickerAutomaticHome(home, current, os.Geteuid(), sudo); err != nil {
			return nil, err
		}
		if profileOverride != "" {
			return nil, errors.New("automatic shell setup does not accept a profile override")
		}
	} else if shell == "" {
		shell = cliterminal.ParentShell(ctx)
	}
	if shell == "" || shell == "pwsh" || cliterminal.ShellName(shell) != shell {
		return nil, errors.New("choose a supported shell: bash, zsh, or fish")
	}
	// Fish's startup directory follows XDG on every platform. CLI scripts stay
	// beside picker settings; consent and its decision lock use a stable home path.
	fishConfig := os.Getenv("XDG_CONFIG_HOME")
	if fishConfig == "" {
		fishConfig = filepath.Join(home, ".config")
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return nil, errors.New("cannot determine the shell integration directory")
	}
	paths := imagePickerShellPaths{home: home, config: config, fishConfig: fishConfig, zdotdir: os.Getenv("ZDOTDIR")}
	targets, err := imagePickerSelectShellTargets(shell, profileOverride, paths)
	if err != nil {
		return nil, err
	}
	if shell == "fish" {
		optOutPath, err := imagePickerTabChoicePath(shell)
		if err != nil {
			return nil, err
		}
		for index := range targets {
			targets[index].OptOutPath = optOutPath
		}
	}
	return targets, nil
}

func imagePickerAutomaticHome(home string, current *user.User, euid int, sudo bool) error {
	if euid == 0 || current.Uid == "0" || sudo {
		return errors.New("automatic shell setup is skipped for root or sudo")
	}
	if !imagePickerAbsolutePath(home) || !imagePickerAbsolutePath(current.HomeDir) || !imagePickerSameHome(home, current.HomeDir) {
		return errors.New("automatic shell setup requires the current user's home directory")
	}
	return nil
}

// Windows may spell the same directory with different casing or short names.
// Compare filesystem identities instead of assuming case folding is safe on every
// Windows volume. Unix retains its existing spelling requirement.
func imagePickerSameHome(first, second string) bool {
	if filepath.Clean(first) == filepath.Clean(second) {
		return true
	}
	if runtime.GOOS != "windows" {
		return false
	}
	left, err := os.Stat(first)
	if err != nil || !left.IsDir() {
		return false
	}
	right, err := os.Stat(second)
	return err == nil && right.IsDir() && os.SameFile(left, right)
}

type imagePickerShellPaths struct {
	home, config, fishConfig, zdotdir string
}

func imagePickerSelectShellTargets(shell, override string, paths imagePickerShellPaths) ([]autocomplete.PickerInstallation, error) {
	return autocomplete.SelectShellProfiles(shell, override, autocomplete.ShellProfilePaths{Home: paths.home, Config: paths.config, FishConfig: paths.fishConfig, Zdotdir: paths.zdotdir})
}
func imagePickerShellRemovalTargets(targets []autocomplete.PickerInstallation, profileOverride string) []autocomplete.PickerInstallation {
	return autocomplete.ShellRemovalProfiles(targets, profileOverride)
}
func imagePickerAbsolutePath(path string) bool { return autocomplete.AbsoluteShellPath(path) }
