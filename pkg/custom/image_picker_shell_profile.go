package custom

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/openai/openai-cli/internal/autocomplete"
)

func imagePickerShellTarget(ctx context.Context, shell string, automatic bool, profileOverride string) ([]autocomplete.PickerInstallation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if automatic {
		if shell == "" {
			// Installer setup uses the preferred login shell, never executing SHELL.
			if preferred := os.Getenv("SHELL"); imagePickerAbsolutePath(preferred) {
				shell = imagePickerShellName(preferred)
			}
			if shell == "" && imagePickerParentShell(ctx) == "pwsh" {
				return nil, nil
			}
		}
		if shell == "pwsh" {
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
		shell = imagePickerParentShell(ctx)
	}
	if shell == "" || shell == "pwsh" || imagePickerShellName(shell) != shell {
		return nil, errors.New("choose a supported shell: bash, zsh, or fish")
	}
	config := os.Getenv("XDG_CONFIG_HOME")
	// Fish's startup directory follows XDG on every platform. Windows CLI
	// scripts and decision locks stay together with preferences in AppData.
	fishConfig := config
	if fishConfig == "" {
		fishConfig = filepath.Join(home, ".config")
	}
	if runtime.GOOS == "windows" {
		config, err = os.UserConfigDir()
		if err != nil {
			return nil, errors.New("cannot determine the shell integration directory")
		}
	} else if config == "" {
		config = fishConfig
	}
	paths := imagePickerShellPaths{home: home, config: config, fishConfig: fishConfig, zdotdir: os.Getenv("ZDOTDIR")}
	return imagePickerSelectShellTargets(shell, profileOverride, paths)
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

// Selection only inspects file names. Ownership, symlinks, concurrent changes,
// and safe file creation are checked by autocomplete when installing.
func imagePickerSelectShellTargets(shell, override string, paths imagePickerShellPaths) ([]autocomplete.PickerInstallation, error) {
	if shell != "bash" && shell != "zsh" && shell != "fish" {
		return nil, fmt.Errorf("unsupported picker shell %q", shell)
	}
	if !imagePickerAbsolutePath(paths.home) || !imagePickerAbsolutePath(paths.config) {
		return nil, errors.New("shell setup requires absolute HOME and configuration paths")
	}
	var profiles []string
	if override != "" {
		absolute, err := filepath.Abs(override)
		if err != nil || !imagePickerAbsolutePath(absolute) {
			return nil, errors.New("profile override must be a valid file path without line breaks")
		}
		profiles = []string{absolute}
	} else {
		switch shell {
		case "bash":
			// Bash login sessions read only the first existing login file.
			// Install into .bashrc too for interactive non-login sessions.
			login := filepath.Join(paths.home, ".bash_profile")
			for _, name := range []string{".bash_profile", ".bash_login", ".profile"} {
				candidate := filepath.Join(paths.home, name)
				_, err := os.Lstat(candidate)
				if err == nil {
					login = candidate
					break
				}
				if !errors.Is(err, os.ErrNotExist) {
					return nil, errors.New("cannot inspect the existing Bash login startup file")
				}
			}
			profiles = []string{filepath.Join(paths.home, ".bashrc"), login}
		case "zsh":
			directory := paths.zdotdir
			if directory == "" {
				directory = paths.home
			}
			if !imagePickerAbsolutePath(directory) {
				return nil, errors.New("ZDOTDIR must be absolute; specify --profile for a relative ZDOTDIR")
			}
			profiles = []string{filepath.Join(directory, ".zshrc")}
		case "fish":
			directory := paths.fishConfig
			if directory == "" {
				directory = paths.config
			}
			if !imagePickerAbsolutePath(directory) {
				return nil, errors.New("fish configuration directory must be an absolute native path; specify --profile for another startup file")
			}
			profiles = []string{filepath.Join(directory, "fish", "conf.d", "openai-picker.fish")}
		}
	}
	result := make([]autocomplete.PickerInstallation, 0, len(profiles))
	for _, profile := range profiles {
		result = append(result, autocomplete.PickerInstallation{
			Shell: autocomplete.CompletionStyle(shell), Directory: filepath.Join(paths.config, "openai", "shell"), Profile: filepath.Clean(profile),
		})
	}
	return result, nil
}

func imagePickerShellRemovalTargets(targets []autocomplete.PickerInstallation, profileOverride string) []autocomplete.PickerInstallation {
	if len(targets) == 0 || targets[0].Shell != autocomplete.CompletionStyleBash || profileOverride != "" {
		return targets
	}
	// A newer login profile can mask the file selected during installation.
	// Default Bash selection starts with HOME/.bashrc; remove owned setup from
	// every known startup file, while RemovePicker preserves unrelated content.
	target := targets[0]
	home := filepath.Dir(target.Profile)
	result := make([]autocomplete.PickerInstallation, 0, 4)
	for _, name := range []string{".bashrc", ".bash_profile", ".bash_login", ".profile"} {
		target.Profile = filepath.Join(home, name)
		result = append(result, target)
	}
	return result
}

func imagePickerAbsolutePath(path string) bool {
	return filepath.IsAbs(path) && utf8.ValidString(path) && !strings.ContainsAny(path, "\x00\r\n")
}
