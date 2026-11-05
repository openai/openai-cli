package autocomplete

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// WithSuggestionSetup serializes the installer with explicit enable and removal.
// The preference follows the user's shell even when XDG_CONFIG_HOME changes.
func WithSuggestionSetup(ctx context.Context, shell CompletionStyle, automatic, remove bool, change func() error) (skipped bool, err error) {
	if shell != CompletionStyleZsh && shell != CompletionStyleBash && shell != CompletionStyleFish {
		return false, errors.New("unsupported command insertion shell")
	}
	home, err := os.UserHomeDir()
	if err != nil || !AbsoluteShellPath(home) {
		return false, errors.New("cannot locate the command insertion preference")
	}
	name := "command-insertion.off-" + string(shell)
	attempted := false
	err = withPickerSetupRoot(ctx, filepath.Join(home, ".openai", "shell"), func(root *os.Root) error {
		marker, err := readPickerFile(ctx, root, name)
		if err != nil {
			return err
		}
		if len(marker.data) != 0 {
			return errors.New("cannot verify the command insertion preference")
		}
		if automatic && marker.info != nil {
			skipped = true
			return nil
		}
		if remove && marker.info == nil {
			file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				return err
			}
			if err := file.Close(); err != nil {
				return err
			}
		}
		attempted = true
		if err := change(); err != nil {
			return err
		}
		if !remove && marker.info != nil {
			return removePickerSnapshot(ctx, root, name, marker)
		}
		return nil
	})
	// A damaged preference must not prevent cleanup of a valid managed profile.
	if remove && err != nil && !attempted && ctx.Err() == nil {
		err = errors.Join(err, change())
	}
	return skipped, err
}

// SuggestionLoader returns the same startup code as the managed profile.
// The installer checks the final line before loading it in its calling shell.
func SuggestionLoader(options SuggestionInstallation) (string, error) {
	data, err := renderInstalledSuggestions(options.installation())
	if err != nil {
		return "", err
	}
	return string(data) + "\n# openai-cli-install-suggestions-complete v1\n", nil
}

const suggestionsIntegration = "suggestions"

// SuggestionInstallation selects a profile and a logical executable path.
// Executable deliberately retains symlinks, so package upgrades remain visible.
type SuggestionInstallation struct {
	Shell      CompletionStyle
	Directory  string
	Profile    string
	Executable string
	DoubleTab  bool
	// Preserve an existing binding preference when enabling insertion from Explore.
	KeepBindings bool
	// Source the loader after the installer has restored PATH.
	AfterShellSetup bool
}

// SuggestionStatus describes verified persistent configuration, not current bindings.
type SuggestionStatus struct {
	Installed   bool
	NeedsUpdate bool
	Executable  string
	ScriptPath  string
	DoubleTab   bool
}

func (options SuggestionInstallation) installation() PickerInstallation {
	return PickerInstallation{
		Shell: options.Shell, Directory: options.Directory, Profile: options.Profile,
		integration: suggestionsIntegration, executable: options.Executable, doubleTab: options.DoubleTab, keepBindings: options.KeepBindings, afterShellSetup: options.AfterShellSetup,
	}
}

// InstallSuggestions uses the same profile transaction and locks as picker setup.
// Its separate namespace preserves picker configuration and cleanup ownership.
func InstallSuggestions(ctx context.Context, options SuggestionInstallation) (PickerInstallResult, error) {
	if !validSuggestionExecutable(options.Executable) {
		return PickerInstallResult{}, errors.New("command insertion setup requires a clean absolute executable path without control characters")
	}
	return changePickerInstallation(ctx, options.installation(), false)
}

// RemoveSuggestions does not need the executable to remain installed.
func RemoveSuggestions(ctx context.Context, options SuggestionInstallation) (PickerInstallResult, error) {
	return changePickerInstallation(ctx, options.installation(), true)
}

// IsSuggestionsInstalled checks owned startup configuration without writing files.
// It reads saved settings, so callers need not rediscover the original executable.
func IsSuggestionsInstalled(ctx context.Context, options SuggestionInstallation) (bool, error) {
	status, err := InspectSuggestions(ctx, options)
	return status.Installed && !status.NeedsUpdate, err
}

func InspectSuggestions(ctx context.Context, options SuggestionInstallation) (SuggestionStatus, error) {
	block, current, err := inspectPickerInstallation(ctx, options.installation())
	if err != nil || block == nil {
		return SuggestionStatus{}, err
	}
	return SuggestionStatus{
		Installed: true, NeedsUpdate: !current, Executable: block.Executable,
		ScriptPath: block.Script, DoubleTab: block.DoubleTab,
	}, nil
}

func validSuggestionExecutable(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && utf8.ValidString(path) &&
		!strings.ContainsFunc(path, func(r rune) bool { return r < 32 || r == 127 })
}

func pickerIntegrationNamespace(integration string) string {
	if integration == suggestionsIntegration {
		return suggestionsIntegration
	}
	return "picker"
}

func pickerIntegrationHeader(integration string) string {
	if integration == suggestionsIntegration {
		return "# OpenAI CLI command insertion startup v1\n"
	}
	return pickerScriptHeader
}

func pickerIntegrationMarkers(integration string) (begin, end, metadata, marker string) {
	if integration == suggestionsIntegration {
		return "# >>> openai command insertion v1 >>>", "# <<< openai command insertion v1 <<<",
			"# openai-suggestions: ", "openai command insertion"
	}
	return pickerBlockBegin, pickerBlockEnd, "# openai-picker: ", "openai image picker"
}

// Two managed integrations can share a startup file that originally did not exist.
// Both retain that fact, so removing the last block restores the original absence.
func siblingCreatedProfile(data []byte, options PickerInstallation) bool {
	if options.integration == suggestionsIntegration {
		options.integration = ""
	} else {
		options.integration = suggestionsIntegration
	}
	_, _, block, err := parsePickerBlock(data, options)
	return err == nil && block != nil && block.ProfileCreated
}

func renderInstalledSuggestions(options PickerInstallation) ([]byte, error) {
	if !validSuggestionExecutable(options.executable) {
		return nil, errors.New("command insertion setup requires a clean absolute executable path without control characters")
	}
	name := map[CompletionStyle]string{
		CompletionStyleBash: "bash_command_insertion_loader.bash",
		CompletionStyleZsh:  "zsh_command_insertion_loader.zsh",
		CompletionStyleFish: "fish_command_insertion_loader.fish",
	}[options.Shell]
	if name == "" {
		return nil, errors.New("command insertion setup supports Bash 4.3+, zsh, and fish")
	}
	script, err := autoCompleteFS.ReadFile("shellscripts/" + name)
	if err != nil {
		return nil, err
	}
	doubleTab := ""
	if options.doubleTab {
		doubleTab = " --suggestions-double-tab"
	}
	content := strings.NewReplacer(
		"__EXECUTABLE__", quotePickerPath(options.Shell, options.executable),
		"__DOUBLE_TAB_ARGUMENT__", doubleTab,
	).Replace(string(script))
	return []byte(pickerIntegrationHeader(options.integration) + content), nil
}
