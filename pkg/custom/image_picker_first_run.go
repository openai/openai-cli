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

// A stalled filesystem operation must not accumulate workers if setup is
// requested again in the same process. The worker releases its slot on return.
var imagePickerFirstRunWorker = make(chan struct{}, 1)

// SetupImagePickerShellOnFirstRun quietly prepares future interactive shells.
// It does not change the current command, terminal bindings, or exit status.
// Failures remain optional, and later ordinary runs may safely retry setup.
func SetupImagePickerShellOnFirstRun(ctx context.Context, args []string) {
	inputTTY, outputTTY, errorTTY := term.IsTerminal(os.Stdin.Fd()), term.IsTerminal(os.Stdout.Fd()), term.IsTerminal(os.Stderr.Fd())
	if ctx.Err() != nil || !inputTTY || !outputTTY || !errorTTY {
		return
	}
	args = append([]string(nil), args...)
	waitForImagePickerFirstRun(ctx, func(ctx context.Context) {
		setupImagePickerShellOnFirstRun(ctx, args)
	})
}

// Filesystem calls cannot in general be interrupted by a context. Keep every
// such call off the foreground command path and bound only the foreground wait.
// A syscall already in progress may finish later; subsequent transaction steps
// check cancellation, and cleanup may remove uncommitted owned artifacts.
func waitForImagePickerFirstRun(ctx context.Context, setup func(context.Context)) {
	if ctx.Err() != nil {
		return
	}
	select {
	case imagePickerFirstRunWorker <- struct{}{}:
	default:
		return
	}
	ctx, cancel := context.WithTimeout(ctx, imagePickerFirstRunTimeout)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { <-imagePickerFirstRunWorker }()
		if ctx.Err() == nil {
			setup(ctx)
		}
	}()
	select {
	case <-ctx.Done():
	case <-done:
	}
}

func setupImagePickerShellOnFirstRun(ctx context.Context, args []string) {
	// The immediate shell may differ from the account's login shell.
	shell := imagePickerParentShell(ctx)
	if !imagePickerFirstRunEligible(args, os.Getenv, true, true, true, shell) {
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

func imagePickerFirstRunEligible(args []string, getenv func(string) string, inputTTY, outputTTY, errorTTY bool, shell string) bool {
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
