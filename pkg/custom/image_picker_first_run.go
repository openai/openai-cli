package custom

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/autocomplete"
)

const imagePickerFirstRunTimeout = 250 * time.Millisecond

// SetupImagePickerShellOnFirstRun quietly prepares future interactive shells.
// It does not change the current command, terminal bindings, or exit status.
// Failures remain optional, and later ordinary runs may safely retry setup.
func SetupImagePickerShellOnFirstRun(ctx context.Context, args []string) {
	inputTTY, outputTTY, errorTTY := term.IsTerminal(os.Stdin.Fd()), term.IsTerminal(os.Stdout.Fd()), term.IsTerminal(os.Stderr.Fd())
	if ctx.Err() != nil || !inputTTY || !outputTTY || !errorTTY {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, imagePickerFirstRunTimeout)
	defer cancel()
	// The immediate shell may differ from the account's login shell.
	shell := imagePickerParentShell(ctx)
	if !imagePickerFirstRunEligible(args, os.Getenv, inputTTY, outputTTY, errorTTY, shell) {
		return
	}
	executable, err := os.Executable()
	if err != nil {
		return
	}
	onPath, err := exec.LookPath("openai")
	if err != nil || !imagePickerSameExecutable(executable, onPath) {
		return
	}
	targets, err := imagePickerShellTarget(ctx, shell, true, "")
	if err != nil {
		return
	}
	missing, err := imagePickerFirstRunMissing(ctx, targets)
	if err != nil || len(missing) == 0 {
		return
	}
	_ = setupImagePickerFirstRun(ctx, targets)
}

func imagePickerFirstRunEligible(args []string, getenv func(string) string, inputTTY, outputTTY, errorTTY bool, shell string) bool {
	switch shell {
	case "bash", "zsh", "fish":
	default:
		return false
	}
	if len(args) == 0 || !inputTTY || !outputTTY || !errorTTY || strings.EqualFold(getenv("TERM"), "dumb") ||
		getenv("OPENAI_PICKER_SHELL") != "" || getenv("OPENAI_PICKER_INTEGRATION") == shell {
		return false
	}
	for _, name := range []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI", "TF_BUILD", "BUILDKITE", "JENKINS_URL", "TEAMCITY_VERSION"} {
		value := strings.ToLower(getenv(name))
		if value != "" && value != "false" && value != "0" {
			return false
		}
	}
	// Conservatively ignore internal operations anywhere in argv. Inspecting
	// these fixed tokens never parses, consumes, or rewrites request arguments.
	for _, argument := range args[1:] {
		switch argument {
		case "__complete", "@completion", "@manpages", "--generate-shell-completion":
			return false
		}
	}
	return true
}

func imagePickerSameExecutable(executable, onPath string) bool {
	current, err := os.Stat(executable)
	if err != nil || !current.Mode().IsRegular() {
		return false
	}
	resolved, err := os.Stat(onPath)
	return err == nil && resolved.Mode().IsRegular() && os.SameFile(current, resolved)
}

func setupImagePickerFirstRun(ctx context.Context, targets []autocomplete.PickerInstallation) error {
	missing, err := imagePickerFirstRunMissing(ctx, targets)
	if err != nil || len(missing) == 0 {
		return err
	}
	return autocomplete.WithPickerSetupLock(ctx, targets[0].Directory, func() error {
		// Recheck preference and every startup file after taking the same
		// decision lock as explicit installation/removal. An opt-out or a
		// changed profile while waiting must not be overwritten.
		missing, err := imagePickerFirstRunMissing(ctx, targets)
		if err != nil {
			return err
		}
		for _, target := range missing {
			if _, err := autocomplete.InstallPicker(ctx, target); err != nil {
				return err
			}
		}
		return nil
	})
}

func imagePickerFirstRunMissing(ctx context.Context, targets []autocomplete.PickerInstallation) ([]autocomplete.PickerInstallation, error) {
	if err := ctx.Err(); err != nil || len(targets) == 0 {
		return nil, err
	}
	for _, target := range targets {
		if target.Shell == autocomplete.CompletionStylePowershell {
			return nil, nil
		}
	}
	declined, err := imagePickerTabDeclined(string(targets[0].Shell))
	if err != nil || declined {
		return nil, err
	}
	var missing []autocomplete.PickerInstallation
	for _, target := range targets {
		installed, err := autocomplete.IsPickerInstalled(ctx, target)
		if err != nil {
			return nil, err
		}
		if !installed {
			missing = append(missing, target)
		}
	}
	return missing, nil
}
