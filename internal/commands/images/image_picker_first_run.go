package images

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/openai/openai-cli/internal/autocomplete"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
)

// SetupShellOnFirstRun performs optional setup after assembly admits a background worker.
func SetupShellOnFirstRun(ctx context.Context, args []string) {
	// The immediate shell may differ from the account's login shell.
	shell := cliterminal.ParentShell(ctx)
	if !FirstRunEligible(args, os.Getenv, true, true, true, shell) {
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
	_ = setupImagePickerFirstRun(ctx, targets)
}

// FirstRunEligible checks shell and invocation eligibility without consuming arguments.
func FirstRunEligible(args []string, getenv func(string) string, inputTTY, outputTTY, errorTTY bool, shell string) bool {
	switch shell {
	case "bash", "zsh", "fish":
	default:
		return false
	}
	if len(args) == 0 || !inputTTY || !outputTTY || !errorTTY || strings.EqualFold(getenv("TERM"), "dumb") ||
		getenv("OPENAI_PICKER_SHELL") != "" {
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
		case "__complete", "@completion", "@manpages", "--generate-shell-completion", "tokenizer", "codex":
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
	if err := ctx.Err(); err != nil || len(targets) == 0 {
		return err
	}
	for _, target := range targets {
		if target.Shell == autocomplete.CompletionStylePowershell {
			return nil
		}
	}
	missing, err := imagePickerFirstRunMissing(ctx, targets)
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		legacy, err := legacyImagePickerTabChoicePath(string(targets[0].Shell))
		if err != nil {
			return err
		}
		declined, err := readImagePickerTabDecline(legacy)
		if err != nil || !declined {
			return err
		}
	}
	directory, err := imagePickerTabChoiceDirectory()
	if err != nil {
		return err
	}
	return autocomplete.WithPickerSetupLock(ctx, directory, func() error {
		if err := migrateImagePickerTabDecline(ctx, string(targets[0].Shell)); err != nil {
			return err
		}
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
