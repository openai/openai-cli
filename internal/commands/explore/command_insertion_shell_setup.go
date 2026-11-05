package explore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/openai/openai-cli/internal/autocomplete"
	localcommands "github.com/openai/openai-cli/internal/commands"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"github.com/urfave/cli/v3"
)

// Keep setup diagnostics fixed and retain error identity without exposing paths.
type commandInsertionSetupError struct {
	message string
	cause   error
	code    int
}

func (e *commandInsertionSetupError) Error() string { return e.message }
func (e *commandInsertionSetupError) Unwrap() error { return e.cause }
func (e *commandInsertionSetupError) ExitCode() int {
	if errors.Is(e.cause, context.Canceled) {
		return 130
	}
	return e.code
}

func insertionSetupFailure(message string, cause error, code int) error {
	return &commandInsertionSetupError{message: message, cause: &localcommands.LocalError{Message: message, Cause: cause}, code: code}
}

type commandInsertionSetupResult struct {
	Action            string `json:"action"`
	Shell             string `json:"shell"`
	Profiles          int    `json:"profiles"`
	Installed         int    `json:"installed"`
	Changed           bool   `json:"changed"`
	NeedsUpdate       bool   `json:"needs_update"`
	DoubleTab         bool   `json:"double_tab"`
	doubleTabProfiles int
}

func commandInsertionShellSetup(ctx context.Context, command *cli.Command) error {
	if err := ctx.Err(); err != nil {
		return insertionSetupFailure("Command insertion setup was interrupted.", err, 1)
	}
	install, remove, inspect := command.Bool("install-suggestions"), command.Bool("uninstall-suggestions"), command.Bool("suggestions-status")
	automatic := command.Bool("auto-suggestions")
	count := 0
	for _, selected := range []bool{install, remove, inspect, automatic} {
		if selected {
			count++
		}
	}
	if count != 1 || command.Args().Len() > 1 || command.Bool("install-picker") || command.Bool("uninstall-picker") ||
		command.Bool("automatic") || command.Bool("picker") || command.Bool("suggestions-click") || command.Bool("suggestions-only") || command.IsSet("suggestions-adapter") ||
		command.IsSet("profile") && command.String("profile") == "" || !install && command.Bool("suggestions-double-tab") ||
		automatic && (command.Bool("suggestions") || command.Args().Len() != 1) {
		return insertionSetupFailure("Choose one command insertion setup action. Picker and mouse setup are separate.", nil, 2)
	}
	format := strings.ToLower(command.Root().String("format"))
	if (format != "" && format != "auto" && format != "text" && format != "json" && format != "jsonl") ||
		command.Root().String("transform") != "" || command.Root().Bool("raw-output") {
		return insertionSetupFailure("Command insertion setup supports auto, text, json, and jsonl output. Transformations and raw output are unavailable.", nil, 2)
	}
	if automatic && format != "" && format != "auto" && format != "text" {
		return insertionSetupFailure("Shell startup code requires text output.", nil, 2)
	}
	if (install || automatic) && runtime.GOOS == "windows" {
		return insertionSetupFailure("Command insertion needs a Unix CLI build. On Windows, use the Linux CLI inside WSL.", nil, 2)
	}
	shell := command.Args().First()
	if shell == "" {
		shell = cliterminal.ParentShell(ctx)
	}
	if shell != "bash" && shell != "zsh" && shell != "fish" {
		return insertionSetupFailure("Choose Bash 4.3+, zsh, or fish for command insertion setup.", ctx.Err(), 2)
	}
	profile := command.String("profile")
	if shell == "zsh" && profile == "" && !autocomplete.AbsoluteShellPath(os.Getenv("ZDOTDIR")) {
		return insertionSetupFailure(`For zsh, specify the current startup file: --profile "${ZDOTDIR-$HOME}/.zshrc".`, nil, 2)
	}
	targets, err := commandInsertionShellTargets(ctx, shell, profile, remove)
	if err != nil {
		return insertionSetupFailure("Could not select command insertion startup files. Check the shell and --profile option.", err, 2)
	}
	action := "status"
	if automatic {
		action = "auto"
	} else if install {
		action = "install"
		for i := range targets {
			targets[i].DoubleTab = command.Bool("suggestions-double-tab")
		}
	} else if remove {
		action = "uninstall"
	}
	return applyCommandInsertionSetup(ctx, command.Root().Writer, format, shell, action, targets)
}

func applyCommandInsertionSetup(ctx context.Context, out io.Writer, format, shell, action string, targets []autocomplete.SuggestionInstallation) error {
	result := commandInsertionSetupResult{Action: action, Shell: shell, Profiles: len(targets)}
	if action == "install" || action == "auto" {
		executable, err := commandInsertionExecutable()
		if err != nil {
			return insertionSetupFailure("Run setup through the installed openai command on PATH. Temporary go run builds cannot install startup integration.", err, 2)
		}
		for index := range targets {
			targets[index].Executable = executable
			if action == "auto" {
				targets[index].KeepBindings = true
				targets[index].AfterShellSetup = true
			}
		}
	}
	if action == "status" {
		if err := inspectOrChangeInsertionTargets(ctx, targets, &result); err != nil {
			return err
		}
	} else {
		skipped, err := autocomplete.WithSuggestionSetup(ctx, autocomplete.CompletionStyle(shell), action == "auto", action == "uninstall", func() error {
			return inspectOrChangeInsertionTargets(ctx, targets, &result)
		})
		if err != nil {
			var setupErr *commandInsertionSetupError
			if errors.As(err, &setupErr) {
				return err
			}
			return insertionSetupFailure("Could not finish command insertion setup. Check your shell settings and retry.", err, 1)
		}
		if skipped {
			return nil
		}
	}
	if action == "auto" {
		if result.Installed != len(targets) || result.NeedsUpdate {
			return insertionSetupFailure("Could not verify the installed command insertion hook. Rerun setup.", nil, 1)
		}
		target := targets[0]
		// Bash login and interactive startup files can have different bindings.
		// Only enable this optional binding now if every profile requested it.
		target.DoubleTab = result.doubleTabProfiles == len(targets)
		loader, err := autocomplete.SuggestionLoader(target)
		if err != nil {
			return insertionSetupFailure("Could not prepare the command insertion hook.", err, 1)
		}
		if out == nil {
			out = os.Stdout
		}
		n, err := io.WriteString(out, loader)
		if err == nil && n != len(loader) {
			err = io.ErrShortWrite
		}
		if err != nil {
			return insertionSetupFailure("Shell setup finished, but its startup code could not be written.", err, 1)
		}
		return nil
	}
	if err := writeCommandInsertionSetup(out, format, result); err != nil {
		return insertionSetupFailure("Command insertion setup finished, but its result could not be displayed. Use --suggestions-status to inspect it.", err, 1)
	}
	return nil
}

func inspectOrChangeInsertionTargets(ctx context.Context, targets []autocomplete.SuggestionInstallation, result *commandInsertionSetupResult) error {
	// The shared engine locks each profile across validation, publication, and recovery.
	// Multiple-profile failures retain successful changes; retrying is idempotent.
	for _, target := range targets {
		if result.Action != "status" {
			var changed autocomplete.PickerInstallResult
			var err error
			if result.Action != "uninstall" {
				changed, err = autocomplete.InstallSuggestions(ctx, target)
			} else {
				changed, err = autocomplete.RemoveSuggestions(ctx, target)
			}
			result.Changed = result.Changed || changed.Changed
			if err != nil {
				message := "Could not finish command insertion setup. Modified files are preserved; check startup-file permissions and retry."
				if result.Changed {
					message = "Some startup files changed before command insertion setup stopped. Check startup-file permissions and retry."
				}
				return insertionSetupFailure(message, err, 1)
			}
		}
		status, statusErr := autocomplete.InspectSuggestions(ctx, target)
		if statusErr != nil {
			return insertionSetupFailure("Could not verify command insertion setup. Check startup-file permissions and managed file changes.", statusErr, 1)
		}
		if status.Installed {
			result.Installed++
		}
		result.NeedsUpdate = result.NeedsUpdate || status.NeedsUpdate
		if status.DoubleTab {
			result.DoubleTab = true
			result.doubleTabProfiles++
		}
	}
	return nil
}

func commandInsertionShellTargets(ctx context.Context, shell, profile string, remove bool) ([]autocomplete.SuggestionInstallation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if runtime.GOOS == "windows" && profile == "" {
		// Unix shells can use a different HOME from Windows USERPROFILE.
		// Status and removal must not silently select that other startup file.
		if shellHome := os.Getenv("HOME"); shellHome != "" && filepath.Clean(shellHome) != filepath.Clean(home) {
			left, leftErr := os.Stat(shellHome)
			right, rightErr := os.Stat(home)
			if !autocomplete.AbsoluteShellPath(shellHome) || leftErr != nil || rightErr != nil ||
				!left.IsDir() || !right.IsDir() || !os.SameFile(left, right) {
				return nil, errors.New("Windows shell HOME differs from USERPROFILE; specify the shell startup file with --profile")
			}
		}
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	fishConfig := os.Getenv("XDG_CONFIG_HOME")
	if fishConfig == "" {
		fishConfig = filepath.Join(home, ".config")
	}
	selected, err := autocomplete.SelectShellProfiles(shell, profile, autocomplete.ShellProfilePaths{Home: home, Config: config, FishConfig: fishConfig, Zdotdir: os.Getenv("ZDOTDIR")})
	if err != nil {
		return nil, err
	}
	if remove {
		selected = autocomplete.ShellRemovalProfiles(selected, profile)
	}
	targets := make([]autocomplete.SuggestionInstallation, 0, len(selected))
	for _, selection := range selected {
		if shell == "fish" && profile == "" {
			selection.Profile = filepath.Join(filepath.Dir(selection.Profile), "openai-suggestions.fish")
		}
		targets = append(targets, autocomplete.SuggestionInstallation{
			Shell: selection.Shell, Directory: selection.Directory, Profile: selection.Profile,
		})
	}
	return targets, nil
}

// Select the stable launcher spelling, preserving a package manager's symlink.
// Never pin a versioned executable when PATH already selects this installation.
func commandInsertionExecutable() (string, error) {
	running, err := os.Executable()
	if err != nil {
		return "", err
	}
	launcher, err := exec.LookPath("openai")
	if err != nil {
		return "", err
	}
	return verifyCommandInsertionExecutable(running, launcher)
}

func verifyCommandInsertionExecutable(running, launcher string) (string, error) {
	if !filepath.IsAbs(launcher) || filepath.Clean(launcher) != launcher || !utf8.ValidString(launcher) ||
		strings.ContainsFunc(launcher, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
		return "", errors.New("unstable executable path")
	}
	current, err := os.Stat(running)
	if err != nil {
		return "", err
	}
	selected, err := os.Stat(launcher)
	if err != nil {
		return "", err
	}
	if !selected.Mode().IsRegular() || !os.SameFile(current, selected) {
		return "", errors.New("PATH selects a different executable")
	}
	// go run executables are disposable, even when a temporary PATH entry finds one.
	for _, path := range []string{running, launcher} {
		parent := filepath.Dir(path)
		key, cached := strings.CutSuffix(filepath.Base(parent), "-d")
		cached = cached && len(key) == 64 && strings.Trim(key, "0123456789abcdef") == "" && filepath.Base(filepath.Dir(parent)) == key[:2]
		if cached || strings.Contains(filepath.ToSlash(path), "/go-build") && filepath.Base(parent) == "exe" {
			return "", errors.New("temporary executable")
		}
	}
	return launcher, nil
}

func writeCommandInsertionSetup(out io.Writer, format string, result commandInsertionSetupResult) error {
	if out == nil {
		out = os.Stdout
	}
	var text string
	if format == "json" || format == "jsonl" {
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		text = string(encoded) + "\n"
	} else {
		switch result.Action {
		case "install":
			text = "Command insertion is set up for future terminals. Updates follow the installed CLI.\nOpen a new terminal. Ctrl+X, then Tab searches suggestions. Select to fill; press Enter separately to run.\n"
			if result.DoubleTab {
				text += "Double Tab is enabled when unbound; it can delay ordinary Tab completion.\n"
			}
		case "uninstall":
			text = "Managed command insertion setup removed. Open a new terminal to finish.\n"
		default:
			text = fmt.Sprintf("Command insertion: %d of %d %s startup files configured.\n", result.Installed, result.Profiles, result.Shell)
			if result.NeedsUpdate {
				text += "Run --install-suggestions to refresh the managed loader.\n"
			}
		}
	}
	n, err := io.WriteString(out, text)
	if err == nil && n != len(text) {
		err = io.ErrShortWrite
	}
	return err
}
