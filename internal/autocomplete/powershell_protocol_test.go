package autocomplete

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestPowerShellFileCompletionPreservesPaths(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("PowerShell is not available")
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	script, err := shellCompletions[CompletionStylePowershell](&cli.Command{}, "openai")
	require.NoError(t, err)
	dir := t.TempDir()
	files := []string{
		"logo.png",
		"assets/logo.png", "assets/long name.png", "assets/l'quote.png", "assets/long dir/child.txt",
		"space dir/logo.png", "apostrophe's/logo.png", "special/log$(not-run).png",
		"wilddir/logo.png", "tick`$dir/logo.png", "tick`?dir/logo.png", "--file=logo.png", "mix'`[dir]/logo.png", "dollar$dir/logo.png", "quote\"dir/logo.png", "assets/l[bracket].png", "assets/l`tick.png", "assets/x`[tick].png", "bracket[dir]/logo.png", "tick`dir/logo.png",
	}
	for _, name := range files {
		if runtime.GOOS == "windows" && strings.ContainsAny(name, `<>:"|?*`) {
			continue
		}
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		require.NoError(t, os.WriteFile(path, nil, 0600))
	}
	// Run the registered completer through PowerShell's real TabExpansion2 and
	// Go backend. Parse completion text without evaluating fixture contents.
	probe := `
function openai {
  & $env:OPENAI_CLI_COMPLETION_BINARY '-test.run=^TestShellCompletionProtocolHelper$' -- openai @args
  $global:LASTEXITCODE = $LASTEXITCODE
}
` + script + `
$word = $env:OPENAI_CLI_COMPLETION_WORD
$assigned = $env:OPENAI_CLI_COMPLETION_ASSIGNED -eq '1'
$line = 'openai --file' + $(if ($assigned) { '=' } else { ' ' }) + $word
$tokens = $null
$errors = $null
$expansion = TabExpansion2 $line $line.Length
$results = @($expansion.CompletionMatches | Where-Object { $_.CompletionText -ne ' ' } | ForEach-Object {
  $completedLine = $line.Substring(0, $expansion.ReplacementIndex) + $_.CompletionText + $line.Substring($expansion.ReplacementIndex + $expansion.ReplacementLength)
  $candidate = [System.Management.Automation.Language.Parser]::ParseInput($completedLine, [ref]$tokens, [ref]$errors)
  $elements = $candidate.EndBlock.Statements[0].PipelineElements[0].CommandElements
  $count = if ($assigned) { 2 } else { 3 }
  $literal = $elements.Count -eq $count -and $errors.Count -eq 0 -and $elements[-1] -is [System.Management.Automation.Language.StringConstantExpressionAst]
  $value = if ($literal) { $elements[-1].Value } else { '' }
  if ($assigned) {
    $literal = $literal -and $value.StartsWith('--file=')
    if ($literal) { $value = $value.Substring(7) }
  }
  [pscustomobject]@{ text = $_.CompletionText; value = $value; literal = $literal; exists = $literal -and (Test-Path -LiteralPath $value); container = $_.ResultType -eq 'ProviderContainer' }
})
ConvertTo-Json -InputObject $results -Compress
`
	probePath := filepath.Join(dir, "completion-probe.ps1")
	require.NoError(t, os.WriteFile(probePath, []byte(probe), 0600))
	for _, tc := range []struct {
		name, word string
		want       []string
	}{
		{"drive relative", filepath.VolumeName(dir) + "lo", []string{"logo.png"}},
		{"relative", "assets/lo", []string{"assets/logo.png", "assets/long name.png", "assets/long dir"}},
		{"backslash", `assets\lo`, []string{"assets/logo.png", "assets/long name.png", "assets/long dir"}},
		{"spaces", `'space dir/lo`, []string{"space dir/logo.png"}},
		{"double quotes", `"space dir/lo`, []string{"space dir/logo.png"}},
		{"apostrophe filename", "'assets/l''", []string{"assets/l'quote.png"}},
		{"apostrophe directory", `'apostrophe''s/lo`, []string{"apostrophe's/logo.png"}},
		{"literal shell syntax", "special/lo", []string{"special/log$(not-run).png"}},
		{"bracket filename", "assets/l[", []string{"assets/l[bracket].png"}},
		{"single quoted bracket", "'assets/l[", []string{"assets/l[bracket].png"}},
		{"double quoted bracket", "\"assets/l[", []string{"assets/l[bracket].png"}},
		{"literal backtick", "'assets/l`", []string{"assets/l`tick.png"}},
		{"double quoted backtick", "\"assets/l``", []string{"assets/l`tick.png"}},
		{"backtick before bracket", "assets/x", []string{"assets/x`[tick].png"}},
		{"bracket directory", "'bracket[dir]/lo", []string{"bracket[dir]/logo.png"}},
		{"backtick directory", "'tick`dir/lo", []string{"tick`dir/logo.png"}},
		{"backtick dollar directory", "'tick`$dir/lo", []string{"tick`$dir/logo.png"}},
		{"backtick question directory", "'tick`?dir/lo", []string{"tick`?dir/logo.png"}},
		{"missing literal directory", "'wild[d]ir/lo", nil},
		{"flag-like filename", "--file=lo", []string{"--file=logo.png"}},
		{"combined punctuation directory", "'mix''`[dir]/lo", []string{"mix'`[dir]/logo.png"}},
		{"dollar directory", "'dollar$dir/lo", []string{"dollar$dir/logo.png"}},
		{"quote directory", "'quote\"dir/lo", []string{"quote\"dir/logo.png"}},
		{"directory", "'assets/long dir/", []string{"assets/long dir/child.txt"}},
		{"absolute", filepath.Join(dir, "assets", "lo"), []string{"assets/logo.png", "assets/long name.png", "assets/long dir"}},
	} {
		for _, assigned := range []string{"0", "1"} {
			t.Run(tc.name+"/assigned="+assigned, func(t *testing.T) {
				if tc.name == "drive relative" && runtime.GOOS != "windows" {
					t.Skip("Windows drive-relative syntax")
				}
				if runtime.GOOS == "windows" {
					for _, name := range tc.want {
						if strings.ContainsAny(name, `<>:"|?*`) {
							t.Skip("fixture contains characters forbidden in Windows filenames")
						}
					}
				}
				ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, pwsh, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", probePath)
				command.Dir = dir
				command.Env = append(os.Environ(), "OPENAI_CLI_COMPLETION_HELPER=1", "OPENAI_CLI_COMPLETION_BINARY="+binary, "OPENAI_CLI_COMPLETION_WORD="+tc.word, "OPENAI_CLI_COMPLETION_ASSIGNED="+assigned)
				var stdout, stderr bytes.Buffer
				command.Stdout, command.Stderr = &stdout, &stderr
				require.NoError(t, command.Run(), stderr.String())
				require.Empty(t, stderr.String())
				var results []struct {
					Text, Value                string
					Literal, Exists, Container bool
				}
				require.NoError(t, json.Unmarshal(stdout.Bytes(), &results), stdout.String())
				var names []string
				for _, result := range results {
					require.True(t, result.Literal, "completion is not one literal argument: %q", result.Text)
					require.True(t, result.Exists, "completion lost its path: %q", result.Text)
					name := filepath.ToSlash(result.Value)
					name = strings.TrimPrefix(name, filepath.ToSlash(dir)+"/")
					name = strings.TrimPrefix(name, "./")
					if runtime.GOOS == "windows" && !filepath.IsAbs(name) {
						name = strings.TrimPrefix(name, filepath.VolumeName(name))
					}
					info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name)))
					require.NoError(t, err)
					require.Equal(t, info.IsDir(), result.Container, result.Text)
					names = append(names, strings.TrimSuffix(name, "/"))
				}
				require.ElementsMatch(t, tc.want, names)
			})
		}
	}
}
