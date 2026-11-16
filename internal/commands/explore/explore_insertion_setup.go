package explore

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/openai/openai-cli/internal/autocomplete"
)

type exploreInsertionSetup struct {
	shell   string
	targets []autocomplete.SuggestionInstallation
	prompt  string
}

// Prepare the offer without reading or changing any profile contents. The user
// reviews these exact targets before the normal installer receives them.
func insertionSetupForPicker(ctx context.Context, shell string) exploreInsertionSetup {
	if runtime.GOOS == "windows" || shell != "zsh" && shell != "fish" && shell != "bash" {
		return exploreInsertionSetup{}
	}
	if _, err := commandInsertionExecutable(); err != nil {
		return exploreInsertionSetup{}
	}
	home, err := os.UserHomeDir()
	if err != nil || !autocomplete.AbsoluteShellPath(home) {
		return exploreInsertionSetup{}
	}
	profile := ""
	if _, exported := os.LookupEnv("ZDOTDIR"); shell == "zsh" && !exported {
		// A child cannot read unexported zsh settings. Offer the standard
		// file by name and let the user confirm that their shell reads it.
		profile = filepath.Join(home, ".zshrc")
	}
	targets, err := commandInsertionShellTargets(ctx, shell, profile, false)
	if err != nil || len(targets) == 0 {
		return exploreInsertionSetup{}
	}
	prompt := "Enable automatic prompt insertion for new " + shell + " sessions?"
	if shell == "bash" {
		prompt = "Enable insertion for Bash 4.3+? Ctrl+X, then Tab inserts your selection."
	}
	prompt += "\n\nAdd the shell hook to:"
	for i := range targets {
		path := targets[i].Profile
		if !utf8.ValidString(path) || strings.ContainsFunc(path, func(r rune) bool {
			return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029'
		}) {
			return exploreInsertionSetup{}
		}
		if suffix, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
			path = "~/" + suffix
		}
		prompt += "\n" + path
		targets[i].KeepBindings = true
	}
	if profile != "" {
		prompt += "\nConfirm only if zsh reads this file."
	}
	return exploreInsertionSetup{shell: shell, targets: targets, prompt: prompt}
}
