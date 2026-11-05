package autocomplete

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// ShellProfilePaths supplies explicit paths for shell startup-file selection.
type ShellProfilePaths struct{ Home, Config, FishConfig, Zdotdir string }

// Selection only inspects file names. Ownership, symlinks, concurrent changes,
// and safe file creation are checked by autocomplete when installing.
func SelectShellProfiles(shell, override string, paths ShellProfilePaths) ([]PickerInstallation, error) {
	if shell != "bash" && shell != "zsh" && shell != "fish" {
		return nil, fmt.Errorf("unsupported picker shell %q", shell)
	}
	if !AbsoluteShellPath(paths.Home) || !AbsoluteShellPath(paths.Config) {
		return nil, errors.New("shell setup requires absolute HOME and configuration paths")
	}
	var profiles []string
	if override != "" {
		absolute, err := filepath.Abs(override)
		if err != nil || !AbsoluteShellPath(absolute) {
			return nil, errors.New("profile override must be a valid file path without line breaks")
		}
		profiles = []string{absolute}
	} else {
		switch shell {
		case "bash":
			// Bash login sessions read only the first existing login file.
			// Install into .bashrc too for interactive non-login sessions.
			login := filepath.Join(paths.Home, ".bash_profile")
			for _, name := range []string{".bash_profile", ".bash_login", ".profile"} {
				candidate := filepath.Join(paths.Home, name)
				_, err := os.Lstat(candidate)
				if err == nil {
					login = candidate
					break
				}
				if !errors.Is(err, os.ErrNotExist) {
					return nil, errors.New("cannot inspect the existing Bash login startup file")
				}
			}
			profiles = []string{filepath.Join(paths.Home, ".bashrc"), login}
		case "zsh":
			directory := paths.Zdotdir
			if !AbsoluteShellPath(directory) {
				return nil, errors.New("zsh setup requires an exported absolute ZDOTDIR; otherwise specify the current shell's startup file with --profile")
			}
			profiles = []string{filepath.Join(directory, ".zshrc")}
		case "fish":
			directory := paths.FishConfig
			if directory == "" {
				directory = paths.Config
			}
			if !AbsoluteShellPath(directory) {
				return nil, errors.New("fish configuration directory must be an absolute native path; specify --profile for another startup file")
			}
			profiles = []string{filepath.Join(directory, "fish", "conf.d", "openai-picker.fish")}
		}
	}
	result := make([]PickerInstallation, 0, len(profiles))
	for _, profile := range profiles {
		result = append(result, PickerInstallation{
			Shell: CompletionStyle(shell), Directory: filepath.Join(paths.Config, "openai", "shell"), Profile: filepath.Clean(profile),
		})
	}
	return result, nil
}

func ShellRemovalProfiles(targets []PickerInstallation, profileOverride string) []PickerInstallation {
	if len(targets) == 0 || targets[0].Shell != CompletionStyleBash || profileOverride != "" {
		return targets
	}
	// A newer login profile can mask the file selected during installation.
	// Default Bash selection starts with HOME/.bashrc; remove owned setup from
	// every known startup file, while RemovePicker preserves unrelated content.
	target := targets[0]
	home := filepath.Dir(target.Profile)
	result := make([]PickerInstallation, 0, 4)
	for _, name := range []string{".bashrc", ".bash_profile", ".bash_login", ".profile"} {
		target.Profile = filepath.Join(home, name)
		result = append(result, target)
	}
	return result
}

func AbsoluteShellPath(path string) bool {
	return filepath.IsAbs(path) && utf8.ValidString(path) && !strings.ContainsAny(path, "\x00\r\n")
}
