package terminal

import (
	"io"
	"strings"

	"github.com/charmbracelet/colorprofile"
)

// ColorProfile picks the color profile for --format pretty output written
// to w. Lipgloss v2 styles always render full color escapes, so the output must
// be downsampled for its destination. This matches the lipgloss v1 behavior the
// CLI had before: plain text when w is not a terminal or NO_COLOR is set, and
// color on a terminal. Color overrides never decorate pipes or files.
// Callers supply terminal detection so wrapped output keeps its destination
// identity without exposing feature-specific writer types to this package.
func ColorProfile(w io.Writer, environ []string, terminal bool) colorprofile.Profile {
	if !terminal {
		return colorprofile.NoTTY
	}
	lookup := func(name string) string {
		value := ""
		for _, kv := range environ {
			if v, ok := strings.CutPrefix(kv, name+"="); ok {
				value = v
			}
		}
		return value
	}
	// Lipgloss v1 dropped all styling, including bold, under NO_COLOR, and
	// NO_COLOR won over CLICOLOR_FORCE. colorprofile does neither, so check it
	// here.
	if lookup("FORCE_COLOR") == "0" || lookup("NO_COLOR") != "" {
		return colorprofile.NoTTY
	}
	profile := colorprofile.Detect(w, environ)
	if lookup("FORCE_COLOR") == "1" && profile < colorprofile.ANSI {
		profile = max(colorprofile.Env(environ), colorprofile.ANSI256)
	}
	return profile
}

// AutomaticPresentation permits terminal presentation only in automatic mode
// on a usable terminal outside CI. It does not query terminal state or input.
func AutomaticPresentation(format string, terminal bool, getenv func(string) string) bool {
	if !strings.EqualFold(format, "auto") || !terminal || strings.EqualFold(getenv("TERM"), "dumb") {
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

// DarkBackground reads the explicit COLORFGBG background, defaulting to dark
// without querying the terminal.
func DarkBackground(colorfgbg string) bool {
	// Use only the environment's explicit standard light backgrounds. The dark
	// default matches the image picker's initial theme without reading stdin.
	parts := strings.Split(colorfgbg, ";")
	switch parts[len(parts)-1] {
	case "7", "15", "231", "255":
		return false
	default:
		return true
	}
}
