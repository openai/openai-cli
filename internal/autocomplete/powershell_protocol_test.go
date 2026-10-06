package autocomplete

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
	files := []string{"assets/logo.png", "assets/long name.png", "assets/l'quote.png", "assets/long dir/child.txt", "space dir/logo.png", "apostrophe's/logo.png", "special/log$(not-run).png"}
	for _, name := range files {
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
$line = 'openai --file ' + $word
$tokens = $null
$errors = $null
$results = @((TabExpansion2 $line $line.Length).CompletionMatches | ForEach-Object {
  $candidate = [System.Management.Automation.Language.Parser]::ParseInput(('capture ' + $_.CompletionText), [ref]$tokens, [ref]$errors)
  $elements = $candidate.EndBlock.Statements[0].PipelineElements[0].CommandElements
  $literal = $elements.Count -eq 2 -and $errors.Count -eq 0 -and $elements[1] -is [System.Management.Automation.Language.StringConstantExpressionAst]
  $value = if ($literal) { $elements[1].Value } else { '' }
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
		{"relative", "assets/lo", []string{"assets/logo.png", "assets/long name.png", "assets/long dir"}},
		{"backslash", `assets\lo`, []string{"assets/logo.png", "assets/long name.png", "assets/long dir"}},
		{"spaces", `'space dir/lo`, []string{"space dir/logo.png"}},
		{"double quotes", `"space dir/lo`, []string{"space dir/logo.png"}},
		{"apostrophe filename", "'assets/l''", []string{"assets/l'quote.png"}},
		{"apostrophe directory", `'apostrophe''s/lo`, []string{"apostrophe's/logo.png"}},
		{"literal shell syntax", "special/lo", []string{"special/log$(not-run).png"}},
		{"directory", "'assets/long dir/", []string{"assets/long dir/child.txt"}},
		{"absolute", filepath.Join(dir, "assets", "lo"), []string{"assets/logo.png", "assets/long name.png", "assets/long dir"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, pwsh, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", probePath)
			command.Dir = dir
			command.Env = append(os.Environ(), "OPENAI_CLI_COMPLETION_HELPER=1", "OPENAI_CLI_COMPLETION_BINARY="+binary, "OPENAI_CLI_COMPLETION_WORD="+tc.word)
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
				info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name)))
				require.NoError(t, err)
				require.Equal(t, info.IsDir(), result.Container, result.Text)
				names = append(names, name)
			}
			require.ElementsMatch(t, tc.want, names)
		})
	}
}
