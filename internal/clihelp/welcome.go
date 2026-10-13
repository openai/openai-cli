package clihelp

import (
	"os"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/urfave/cli/v3"
)

// The welcome header adds no input, terminal queries, subprocesses, or state.
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
	if os.Getenv("NO_COLOR") != "" || os.Getenv("FORCE_COLOR") == "0" || os.Getenv("CLICOLOR") == "0" {
		profile = colorprofile.NoTTY
	}
	return renderWelcome(root.Version, helpWidth(root), profile, welcomeDarkBackground(os.Getenv("COLORFGBG")), welcomeUnicode(os.Getenv))
}

// Character encoding and color support are independent. Unknown encodings use
// printable ASCII; explicit locale settings take precedence over terminal hints.
func welcomeUnicode(getenv func(string) string) bool {
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if locale := getenv(name); locale != "" {
			locale = strings.ToUpper(strings.ReplaceAll(locale, "-", ""))
			locale, _, _ = strings.Cut(locale, "@")
			return locale == "UTF8" || strings.HasSuffix(locale, ".UTF8")
		}
	}
	return getenv("WT_SESSION") != ""
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

const (
	welcomeTitle   = "OpenAI CLI"
	welcomeTagline = "What are we making today?"
)

func renderWelcome(version string, width int, profile colorprofile.Profile, dark, unicode bool) string {
	version = welcomeVersion(version)
	if !unicode {
		var ascii strings.Builder
		for _, char := range version {
			if char <= 127 {
				ascii.WriteRune(char)
			} else {
				quoted := strconv.QuoteRuneToASCII(char)
				ascii.WriteString(quoted[1 : len(quoted)-1])
			}
		}
		version = ascii.String()
	}
	required := max(ansi.StringWidth(welcomeTitle+"  "+version), ansi.StringWidth(welcomeTagline))
	if width < 29 || required+4 > width {
		return ""
	}
	focus, muted, frameColor := "#3159BC", "#657087", "#3159BC"
	if dark {
		focus, muted, frameColor = "#8AA8FF", "#A4ACC2", "#5675BC"
	}
	heading := lipgloss.NewStyle().Bold(true)
	note := lipgloss.NewStyle().Foreground(lipgloss.Color(muted))
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
	content := title + "\n" + note.Render(welcomeTagline)
	if width >= 40 {
		content = "\n" + content + "\n"
	}
	// Keep complete metadata beside the artwork. Smaller terminals and long
	// versions use the compact text layout without clipping or wrapping.
	versionWidth := ansi.StringWidth(version)
	wordmark := width >= 56 && versionWidth <= 16
	filled := unicode && profile > colorprofile.ASCII
	filledWidth := max(76, 66+versionWidth)
	if filled {
		wordmark = width >= filledWidth && versionWidth <= 16
	}
	if wordmark {
		padding, cardWidth = 2, 56
		content = "▄▀▀▄ █▀▀▄ █▀▀▀ █▄  █ ▄▀▀▄ ▀█▀   " + heading.Render(welcomeTitle) + "\n" +
			"█  █ █▄▄▀ █▀▀  █ █ █ █▄▄█  █    " + note.Render(version) + "\n" +
			"▀▄▄▀ █    █▄▄▄ █  ▀█ █  █ ▄█▄\n" +
			note.Render(welcomeTagline)
		if filled {
			cardWidth = filledWidth
			// Each bitmap pixel spans two terminal columns. Six rows preserve
			// the reference letter shapes and their open counters.
			rows := []string{
				".##..###..####.#...#..##..###",
				"#..#.#..#.#....##..#.#..#..#.",
				"#..#.#..#.###..#.#.#.#..#..#.",
				"#..#.###..#....#.#.#.####..#.",
				"#..#.#....#....#..##.#..#..#.",
				".##..#....####.#...#.#..#.###",
			}
			// Smaller color steps soften the artwork without changing its pixels.
			colors := []string{focus, "#3C62BF", "#476BC3", "#5173C6", "#5C7CCA", "#6785CD"}
			shadows := []string{"#D8DFF2", "#DAE1F3", "#DCE3F4", "#DEE4F5", "#E0E6F5", "#E2E8F6"}
			trim := "#CDD1D8"
			if dark {
				colors = []string{focus, "#809EF2", "#7594E4", "#6B89D7", "#607FC9", "#5675BC"}
				shadows = []string{"#333D57", "#313B54", "#2F3951", "#2D364E", "#2B344C", "#293249"}
				trim = "#2B3039"
			}
			for i, row := range rows {
				if profile == colorprofile.ANSI {
					colors[i] = focus
					shadows[i], trim = "", ""
				}
				rows[i] = welcomePixelRow(row, colors[i], shadows[i], trim, i == len(rows)-1)
			}
			rows[1] += "  " + heading.Render(welcomeTitle)
			rows[2] += "  " + note.Render(version)
			content = strings.Join(rows, "\n") + "\n" + note.Render(welcomeTagline)
		} else if !unicode {
			content = heading.Render(welcomeTitle) + "\n" + note.Render(version) + "\n\n" + note.Render(welcomeTagline)
		}
	}
	border := lipgloss.NormalBorder()
	if !unicode {
		border = lipgloss.ASCIIBorder()
	}
	frame := lipgloss.NewStyle().Border(border).BorderForeground(lipgloss.Color(frameColor)).Padding(0, padding).Width(cardWidth)
	card := frame.Render(content)
	var out strings.Builder
	writer := colorprofile.Writer{Forward: &out, Profile: profile}
	_, _ = writer.WriteString(card + "\n\n")
	return out.String()
}

// Background fills keep the faces and side shadows continuous between rows.
// Only the bottom edge uses a single-cell glyph; no cursor or font changes occur.
func welcomePixelRow(row, color, shadow, trim string, bottom bool) string {
	face := lipgloss.NewStyle().Background(lipgloss.Color(color))
	pixel := face.Render("  ")
	side := ""
	if shadow != "" {
		side = lipgloss.NewStyle().Background(lipgloss.Color(shadow)).Render(" ")
	}
	if bottom && trim != "" {
		pixel = face.Foreground(lipgloss.Color(trim)).Render("▁▁")
		if shadow != "" {
			// The side mask inherits the terminal background, avoiding bright tails.
			side = lipgloss.NewStyle().Foreground(lipgloss.Color(shadow)).Reverse(true).Render("▁")
		}
	}
	var out strings.Builder
	for i, cell := range row {
		if cell == '#' {
			out.WriteString(pixel)
		} else if side != "" && i > 0 && row[i-1] == '#' {
			out.WriteString(side)
			out.WriteByte(' ')
		} else {
			out.WriteString("  ")
		}
	}
	if strings.HasSuffix(row, "#") {
		out.WriteString(side)
	}
	return out.String()
}

func welcomeVersion(version string) string {
	version = strings.Join(strings.Fields(readable.Text(version)), " ")
	if version != "" && version[0] >= '0' && version[0] <= '9' {
		version = "v" + version
	}
	return version
}
