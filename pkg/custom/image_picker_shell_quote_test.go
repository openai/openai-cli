package custom

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Parse the full printed command in each real shell; never invoke an API.
func TestImagePickerNativeShellQuoting(t *testing.T) {
	values := []string{"", "ordinary", "two words", "a 'quote' and \"double\"", `C:\folder\name`,
		"line one\nline two\tend\r", "雪 🐈", "a\u202eb\u0085c\u200bd", "$(touch NOT_RUN); & | > [x] {x}",
		"$HOME `backtick`", "trailing ", "~", "\x1b]52;c;synthetic\x07", "a\u2028b\u2029c", "\\@literal"}
	for _, shell := range []string{"bash", "zsh", "fish", "pwsh"} {
		t.Run(shell, func(t *testing.T) {
			binary, err := exec.LookPath(shell)
			if err != nil {
				if strings.Contains(","+os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS")+",", ","+shell+",") {
					t.Fatalf("required shell %s unavailable", shell)
				}
				t.Skipf("%s is not installed", shell)
			}
			arguments := append([]string{"images", "generate", "--prompt"}, values...)
			command := formatImagePickerCommand(arguments, shell)
			require.NotContains(t, command, "\x1b")
			require.NotContains(t, command, "\n")
			require.NotContains(t, command, "\u202e")
			prefix := "openai() { printf '%s\\0' \"$@\"; }\n"
			var shellArgs []string
			switch shell {
			case "bash":
				shellArgs = []string{"--noprofile", "--norc", "-c"}
			case "zsh":
				shellArgs = []string{"-f", "-c"}
			case "fish":
				shellArgs = []string{"--no-config", "-c"}
				prefix = "function openai; printf '%s\\0' $argv; end\n"
			case "pwsh":
				shellArgs = []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}
				prefix = "function openai { foreach ($v in $args) { [Console]::Out.Write($v); [Console]::Out.Write([char]0) } }\n"
			}
			dir := t.TempDir()
			process := exec.Command(binary, append(shellArgs, prefix+command)...)
			process.Dir = dir
			process.Env = append(os.Environ(), "HOME="+dir, "XDG_CONFIG_HOME="+dir, "OPENAI_API_KEY=synthetic-not-used", "OPENAI_BASE_URL=http://127.0.0.1:9")
			var stdout, stderr bytes.Buffer
			process.Stdout, process.Stderr = &stdout, &stderr
			require.NoError(t, process.Run(), stderr.String())
			require.Empty(t, stderr.String())
			require.Equal(t, strings.Join(arguments, "\x00")+"\x00", stdout.String(), command)
			_, err = os.Stat(filepath.Join(dir, "NOT_RUN"))
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}
