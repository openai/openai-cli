package clihelp

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"text/template"

	"github.com/urfave/cli/v3"
)

func TestWindowsKeySetupUsesTheShellWhereTheKeyIsEntered(t *testing.T) {
	const name = `C:\tools\Owner's Preview\openai.exe`
	const cmdRun = `"C:\tools\Owner's Preview\openai.exe"`
	const psRun = `& 'C:\tools\Owner''s Preview\openai.exe'`
	for _, tc := range []struct {
		parent, initial string
		transition      bool
	}{
		{`C:\Windows\cmd.exe`, cmdRun, true},
		{"", psRun, true},
		{`C:\Windows\WindowsPowerShell\v1.0\PowerShell.EXE`, psRun, false},
		{`C:\Program Files\PowerShell\7\pwsh.exe`, psRun, false},
	} {
		t.Run(tc.parent, func(t *testing.T) {
			root := &cli.Command{Name: "openai", Metadata: map[string]any{
				"help-invocation": tc.initial, "help-executable-path": name,
			}}
			tmpl := template.Must(template.New("setup").Funcs(template.FuncMap{
				"setupGuide": func(command *cli.Command) setupGuideContext {
					return setupGuideForParent(command, "windows", tc.parent)
				},
				"suggest":    func(_ *cli.Command, text string) string { return text },
				"suggestFor": func(_ *cli.Command, text string, _ ...string) string { return text },
			}).Parse(setupHelp))
			var output strings.Builder
			if err := tmpl.Execute(&output, root); err != nil {
				t.Fatal(err)
			}
			guide := output.String()
			transition := strings.Index(guide, "run powershell first")
			hiddenRead := strings.Index(guide, `Read-Host "API key" -AsSecureString`)
			if (transition >= 0) != tc.transition || hiddenRead < 0 || transition > hiddenRead ||
				!strings.Contains(guide, "$env:OPENAI_API_KEY = ") ||
				!strings.Contains(guide, psRun+" models list") || !strings.Contains(guide, psRun+" help --all setup") {
				t.Fatalf("incorrect shell transition, key entry, or copied verification:\n%s", guide)
			}
		})
	}
}

func TestGuideHelpWrapsProseAndPreservesShellLines(t *testing.T) {
	root := &cli.Command{Name: "openai", Writer: &bytes.Buffer{}}
	command := `$env:OPENAI_API_KEY = [System.Net.NetworkCredential]::new("", $openaiKey).Password`
	prose := "These instructions apply to this terminal session. " + strings.Repeat("Keep the original instructions. ", 4)
	got := formatGuide(root, "SETUP\n\n   "+prose+"\n       "+command+"\n")
	if !strings.Contains(got, "\n      "+command+"\n") {
		t.Fatalf("guide split or changed a shell command: %s", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.TrimSpace(line) == command {
			continue
		}
		if len(line) > 79 || strings.TrimRight(line, " \t") != line {
			t.Fatalf("guide has overflowing prose or trailing whitespace: %q", line)
		}
	}
	if !strings.Contains(strings.Join(strings.Fields(got), " "), strings.Join(strings.Fields(prose), " ")) {
		t.Fatal("guide lost instruction words")
	}
}

func TestGuideHelpStylesLabelsAndSeparatesTerminalPrompt(t *testing.T) {
	const guide = "Shell setup\n\nCOMMANDS\nChoose a command:\n   openai help setup admin\n"
	const plain = "Shell setup\n\nCOMMANDS\nChoose a command:\n  openai help setup admin\n"
	root := &cli.Command{Name: "openai", Writer: &bytes.Buffer{}}
	if got := formatGuide(root, guide); got != plain {
		t.Fatalf("piped guide = %q, want %q", got, plain)
	}
	if !welcomeTerminal(os.Stdout) {
		t.Skip("terminal styles and prompt spacing require a PTY")
	}
	setHelpTerminalColors(t)
	root.Writer = &welcomeTerminalBuffer{}
	style := commandHelpStyle(root)
	want := "\x1b[1mShell setup\x1b[0m\n\n" + style.render(style.section, "COMMANDS") + "\n" +
		style.render(style.note, "Choose a command:") + "\n  openai help setup admin\n\n"
	for _, source := range []string{guide, guide + "\n"} {
		if got := strings.ReplaceAll(formatGuide(root, source), "\x1b[m", "\x1b[0m"); got != strings.ReplaceAll(want, "\x1b[m", "\x1b[0m") {
			t.Fatalf("terminal guide = %q, want %q", got, want)
		}
	}
}
