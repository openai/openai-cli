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
	return renderWelcome(root.Version, helpWidth(root), profile, welcomeDarkBackground(os.Getenv("COLORFGBG")))
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

// Match the image picker, tokenizer, and Codex guide without querying stdin.
func welcomeDarkBackground(colorfgbg string) bool {
	parts := strings.Split(colorfgbg, ";")
	switch parts[len(parts)-1] {
	case "7", "15", "231", "255":
		return false
	default:
		return true
	}
}

const welcomeTitle = "OpenAI CLI"

func renderWelcome(version string, width int, profile colorprofile.Profile, dark bool) string {
	version = strings.Join(strings.Fields(readable.Text(version)), " ")
	if version != "" && version[0] >= '0' && version[0] <= '9' {
		version = "v" + version
	}
	const greeting = "What are we making today?"
	required := max(ansi.StringWidth(welcomeTitle+"  "+version), ansi.StringWidth(greeting))
	if required+4 > width {
		return ""
	}
	// Match the image picker and tokenizer palette without importing their
	// private presentation helpers, which depend on this package.
	focus, muted := "#3159BC", "#657087"
	if dark {
		focus, muted = "#8AA8FF", "#A4ACC2"
	}
	// Preserve the terminal foreground when its background hint is absent.
	heading := lipgloss.NewStyle().Bold(true)
	note := lipgloss.NewStyle().Foreground(lipgloss.Color(muted))
	padding := 1
	if required+6 <= width {
		padding = 2
	}
	// Width includes borders and padding. Keep one title/version row intact.
	cardWidth := min(width, max(40, required+2+2*padding))
	contentWidth := cardWidth - 2 - 2*padding
	frame := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(focus)).Padding(0, padding).Width(cardWidth)
	title := heading.Render(welcomeTitle)
	if version != "" {
		gap := contentWidth - ansi.StringWidth(welcomeTitle) - ansi.StringWidth(version)
		title += strings.Repeat(" ", gap) + note.Render(version)
	}
	var card string
	// Small terminals retain the compact layout; wider terminals get breathing room.
	if width < 40 {
		card = frame.Render(title + "\n" + note.Render(greeting))
	} else {
		card = frame.Padding(1, padding).Render(title + "\n" + note.Render(greeting))
	}
	var out strings.Builder
	writer := colorprofile.Writer{Forward: &out, Profile: profile}
	_, _ = writer.WriteString(card + "\n\n")
	return out.String()
}
