package custom

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
)

// Always show the exact build the user invoked, unless PATH already resolves
// openai to that same executable. A checkout must not test an older install.
func imageInlineExecutable() string {
	executable, err := os.Executable()
	if err != nil || strings.IndexFunc(executable, unicode.IsControl) >= 0 {
		return "openai"
	}
	installed, _ := exec.LookPath("openai")
	cwd, _ := os.Getwd()
	temporary := os.Getenv("GOTMPDIR")
	if temporary == "" {
		temporary = os.TempDir()
	}
	return imageInlineExecutableCommand(executable, installed, cwd, temporary)
}

func imageInlineExecutableCommand(executable, installed, cwd, temporary string) string {
	// `go run` removes this executable when setup exits. Re-enter the same
	// positively identified checkout rather than suggesting its temporary path.
	if isTemporaryImageExecutable(executable, temporary) {
		if checkout := imageInlineCheckout(cwd); checkout != "" {
			return "go -C " + quoteImageShellArgument(checkout) + " run ./cmd/openai"
		}
	}
	if installed != "" {
		a, aerr := os.Stat(executable)
		b, berr := os.Stat(installed)
		if aerr == nil && berr == nil && os.SameFile(a, b) {
			return "openai"
		}
	}
	return quoteImageShellArgument(executable)
}

func isTemporaryImageExecutable(executable, temporary string) bool {
	// Resolve macOS's /var -> /private/var aliases before comparing paths.
	root, err := filepath.EvalSymlinks(temporary)
	if err != nil {
		return false
	}
	path, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if len(parts) != 4 || parts[2] != "exe" || parts[3] != "openai" && parts[3] != "openai.exe" {
		return false
	}
	for i, prefix := range []string{"go-build", "b"} {
		digits, ok := strings.CutPrefix(parts[i], prefix)
		if !ok || digits == "" || strings.Trim(digits, "0123456789") != "" {
			return false
		}
	}
	return true
}

func imageInlineCheckout(cwd string) string {
	if cwd == "" || strings.IndexFunc(cwd, unicode.IsControl) >= 0 {
		return ""
	}
	dir, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			// Stop at the nearest module, including nested modules that should
			// not be mistaken for the CLI checkout surrounding them.
			for _, line := range strings.Split(string(data), "\n") {
				fields := strings.Fields(strings.SplitN(line, "//", 2)[0])
				if len(fields) == 0 {
					continue
				}
				// Require the checkout's normal leading module directive; do
				// not mistake text inside a block comment for module identity.
				if len(fields) != 2 || fields[0] != "module" {
					return ""
				}
				if fields[1] != "github.com/openai/openai-cli" && fields[1] != `"github.com/openai/openai-cli"` {
					return ""
				}
				info, err := os.Stat(filepath.Join(dir, "cmd", "openai"))
				if err == nil && info.IsDir() {
					return dir
				}
				return ""
			}
			return ""
		}
		if !errors.Is(err, os.ErrNotExist) || filepath.Dir(dir) == dir {
			return ""
		}
		dir = filepath.Dir(dir)
	}
}

func quoteImageShellArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
