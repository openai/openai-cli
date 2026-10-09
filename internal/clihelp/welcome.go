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
	required := max(ansi.StringWidth(welcomeTitle+"  "+version), ansi.StringWidth("The OpenAI API"))
	if width < 29 || required+4 > width {
		return ""
	}
	focus, muted, middle, depth := "#3159BC", "#657087", "#466BC6", "#6785CD"
	if dark {
		focus, muted, middle, depth = "#8AA8FF", "#A4ACC2", "#7192E6", "#5675BC"
	}
	// Preserve the existing theme. Limit depth to terminals with enough colors.
	if profile == colorprofile.ANSI {
		middle, depth = focus, focus
	}
	heading := lipgloss.NewStyle().Bold(true)
	note := lipgloss.NewStyle().Foreground(lipgloss.Color(muted))
	blue := lipgloss.NewStyle().Foreground(lipgloss.Color(focus))
	mid := lipgloss.NewStyle().Foreground(lipgloss.Color(middle))
	back := lipgloss.NewStyle().Foreground(lipgloss.Color(depth))
	padding := 1
	if required+6 <= width {
		padding = 2
	}
	cardWidth := min(width, max(40, required+2+2*padding))
	contentWidth := cardWidth - 2 - 2*padding
	title := heading.Render(welcomeTitle)
	if version != "" {
		title += strings.Repeat(" ", contentWidth-ansi.StringWidth(welcomeTitle)-ansi.StringWidth(version)) + note.Render(version)
	}
	content := title + "\n" + note.Render("The OpenAI API")
	if width >= 40 {
		content = "\n" + content + "\n"
	}
	// Wide terminals get a six-row wordmark. Narrow terminals and long versions
	// retain the compact text layout without truncating identity or version.
	if width >= 56 && ansi.StringWidth(version) <= 16 {
		padding, cardWidth = 2, 56
		// Three terminal-cell rows form the wordmark. Keep literal identity and
		// version alongside it so the art never carries essential information.
		content = blue.Render("▄▀▀▄ █▀▀▄ █▀▀▀ █▄  █ ▄▀▀▄ ▀█▀") + "   " + heading.Render(welcomeTitle) + "\n" +
			mid.Render("█  █ █▄▄▀ █▀▀  █ █ █ █▄▄█  █ ") + "   " + note.Render(version) + "\n" +
			back.Render("▀▄▄▀ █    █▄▄▄ █  ▀█ █  █ ▄█▄") + "\n" +
			note.Render("The OpenAI API in your terminal.")
	}
	frame := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(focus)).Padding(0, padding).Width(cardWidth)
	card := frame.Render(content)
	var out strings.Builder
	writer := colorprofile.Writer{Forward: &out, Profile: profile}
	_, _ = writer.WriteString(card + "\n\n")
	return out.String()
}
