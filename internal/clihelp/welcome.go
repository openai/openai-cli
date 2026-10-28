package clihelp

import (
	"os"
	"runtime"
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
	format := root.String("format")
	if format != "" && !strings.EqualFold(format, "auto") && !strings.EqualFold(format, "text") {
		return ""
	}
	input, output := root.Reader, root.Writer
	if input == nil {
		input = os.Stdin
	}
	if output == nil {
		output = os.Stdout
	}
	if !welcomeTerminal(input) || !welcomeTerminal(output) || !welcomeTerminal(os.Stderr) || !welcomeEnvironment(os.Getenv, runtime.GOOS) {
		return ""
	}
	profile := colorprofile.Env(os.Environ()) // Environment only; Detect can launch tmux.
	if runtime.GOOS == "windows" && os.Getenv("TERM") == "" && os.Getenv("WT_SESSION") == "" {
		// Classic consoles do not advertise VT processing. The OS version alone
		// cannot establish that ANSI escapes are enabled on this console.
		profile = colorprofile.NoTTY
	}
	if os.Getenv("NO_COLOR") != "" || os.Getenv("FORCE_COLOR") == "0" || os.Getenv("CLICOLOR") == "0" {
		profile = colorprofile.NoTTY
	}
	return renderWelcome(root.Version, helpWidth(root), profile,
		welcomeDarkBackground(os.Getenv("COLORFGBG")), welcomeUnicode(os.Getenv), os.Getenv("TERM_PROGRAM"))
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
	return ok && (term.IsTerminal(file.Fd()) || welcomePseudoTerminal(file.Fd()))
}

func welcomeEnvironment(getenv func(string) string, goos string) bool {
	terminal := getenv("TERM")
	if terminal == "" && getenv("WT_SESSION") == "" && goos != "windows" || strings.EqualFold(terminal, "dumb") {
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

func renderWelcome(version string, width int, profile colorprofile.Profile, dark, unicode bool, terminalProgram string) string {
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
	detailed := wordmark && filled && profile > colorprofile.ANSI
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
			if dark {
				colors = []string{focus, "#809EF2", "#7594E4", "#6B89D7", "#607FC9", "#5675BC"}
				shadows = []string{"#333D57", "#313B54", "#2F3951", "#2D364E", "#2B344C", "#293249"}
			}
			for i, row := range rows {
				if profile == colorprofile.ANSI {
					colors[i] = focus
					shadows[i] = ""
				}
				rows[i] = welcomePixelRow(row, colors[i], shadows[i], i)
			}
			metadataRow, captionPadding := 1, 0
			if detailed {
				metadataRow, captionPadding = 2, (59-ansi.StringWidth(welcomeTagline))/2
			}
			rows[metadataRow] += "  " + heading.Render(welcomeTitle)
			rows[metadataRow+1] += "  " + note.Render(version)
			content = strings.Join(rows, "\n") + "\n" + strings.Repeat(" ", captionPadding) + note.Render(welcomeTagline)
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
	if detailed {
		// The cursor stamp stays within the last row, without cursor movement.
		edge := lipgloss.NewStyle().Foreground(lipgloss.Color(frameColor))
		stamp := lipgloss.NewStyle().Foreground(lipgloss.Color(focus))
		stroke, rightEdge := "─", "────┘"
		if terminalProgram == "Apple_Terminal" {
			// Match the stamp's font baseline. The box-drawn end cap contains
			// the fallback stroke's ink before it reaches the right corner.
			stroke, rightEdge = "⎯", "⎯⎯⎯─┘"
		}
		card = card[:strings.LastIndexByte(card, '\n')+1] +
			edge.Render("└"+strings.Repeat(stroke, cardWidth-12)) + "  " +
			stamp.Render(">_") + "  " + edge.Render(rightEdge)
	}
	var out strings.Builder
	writer := colorprofile.Writer{Forward: &out, Profile: profile}
	_, _ = writer.WriteString(card + "\n\n")
	return out.String()
}

// Fractional caps and selected P/E/A bars balance the fixed bitmap's weights.
// Reverse masks inherit the terminal background; ANSI16 keeps plain fills.
func welcomePixelRow(row, color, shadow string, line int) string {
	var out strings.Builder
	if shadow == "" {
		pixel := lipgloss.NewStyle().Background(lipgloss.Color(color)).Render("  ")
		for _, cell := range row {
			if cell == '#' {
				out.WriteString(pixel)
			} else {
				out.WriteString("  ")
			}
		}
		return out.String()
	}
	pixel := func(column int, paint string) string {
		style := lipgloss.NewStyle()
		upperBar := line == 2 && column >= 22 && column <= 26 || line == 3 && column >= 44 && column <= 47
		lowerBar := line == 3 && column >= 12 && column <= 16
		if line == 0 || upperBar {
			style = style.Foreground(lipgloss.Color(paint)).Underline(true)
			if upperBar && (column == 22 || column == 44) {
				style = style.Background(lipgloss.Color(shadow))
			}
			return style.Render("▇")
		}
		if line == 5 || lowerBar {
			style = style.Foreground(lipgloss.Color(paint)).Reverse(true).Underline(true)
			if lowerBar && column == 12 {
				style = style.Reverse(false).Foreground(lipgloss.Color(shadow)).Background(lipgloss.Color(paint))
			}
			return style.Render("▂")
		}
		return style.Background(lipgloss.Color(paint)).Render(" ")
	}
	for i, cell := range row {
		if cell == '#' {
			out.WriteString(pixel(i*2, color))
			out.WriteString(pixel(i*2+1, color))
		} else if i > 0 && row[i-1] == '#' {
			out.WriteString(pixel(i*2, shadow))
			out.WriteByte(' ')
		} else {
			out.WriteString("  ")
		}
	}
	if strings.HasSuffix(row, "#") {
		out.WriteString(pixel(len(row)*2, shadow))
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
