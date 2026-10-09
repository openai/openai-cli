package clihelp

import (
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/urfave/cli/v3"
)

// The welcome header adds no input, terminal escape queries, subprocesses, or state.
// Keep the framework's help path so request setup still never runs.
func welcomeHeader(root *cli.Command) string {
	input, output := root.Reader, root.Writer
	if input == nil {
		input = os.Stdin
	}
	if output == nil {
		output = os.Stdout
	}
	if !welcomeTerminal(input) || !welcomeTerminal(output) || !welcomeTerminal(os.Stderr) || !welcomeEnvironment(os.Getenv) {
		return ""
	}
	profile := colorprofile.Env(os.Environ()) // Environment only; Detect can launch tmux.
	if os.Getenv("NO_COLOR") != "" || os.Getenv("FORCE_COLOR") == "0" {
		profile = colorprofile.NoTTY
	}
	return renderWelcome(root.Version, helpWidth(root), profile)
}

func welcomeTerminal(value any) bool {
	file, ok := value.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(file.Fd())
}

func welcomeEnvironment(getenv func(string) string) bool {
	terminal := getenv("TERM")
	if terminal == "" && getenv("WT_SESSION") == "" || strings.EqualFold(terminal, "dumb") {
		return false
	}
	for _, name := range []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI", "TF_BUILD", "BUILDKITE", "JENKINS_URL", "TEAMCITY_VERSION"} {
		value := strings.ToLower(getenv(name))
		if value != "" && value != "false" && value != "0" {
			return false
		}
	}
	return true
}

func renderWelcome(version string, width int, profile colorprofile.Profile) string {
	version = strings.Join(strings.Fields(readable.Text(version)), " ")
	if version != "" && version[0] >= '0' && version[0] <= '9' {
		version = "v" + version
	}
	title := strings.TrimSpace(">_ OpenAI CLI  " + version)
	const greeting = "What are we making today?"
	// Keep this optional header to four lines. Narrow terminals and unusually
	// long build versions retain complete help without clipping the version.
	if max(ansi.StringWidth(title), ansi.StringWidth(greeting))+4 > width {
		return ""
	}
	heading := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.ANSIColor(4))
	frame := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	var out strings.Builder
	writer := colorprofile.Writer{Forward: &out, Profile: profile}
	_, _ = writer.WriteString(frame.Render(heading.Render(title)+"\n"+greeting) + "\n\n")
	return out.String()
}
