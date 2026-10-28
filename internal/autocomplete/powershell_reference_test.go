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

func TestPowerShellReferenceCompletionUsesLiteralPaths(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("PowerShell is not available")
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	script, err := shellCompletions[CompletionStylePowershell](&cli.Command{}, "openai")
	require.NoError(t, err)
	directory := t.TempDir()
	names := []string{"safe.txt", "safe space.txt", "safe'quote.txt", "safe;literal.txt", "safe&literal.txt", "safe`tick.txt", "safe$(not-run).txt", "safe[bracket].txt", "safe\u2019quote.txt"}
	for _, parent := range []string{"", "nested", "bracket[dir]", "tick`dir", "apostrophe's"} {
		for _, name := range names {
			path := filepath.Join(directory, parent, name)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
			require.NoError(t, os.WriteFile(path, nil, 0600))
		}
		require.NoError(t, os.MkdirAll(filepath.Join(directory, parent, "safe dir"), 0700))
	}
	probe := `
function openai {
  & $env:OPENAI_CLI_COMPLETION_BINARY '-test.run=^TestShellCompletionProtocolHelper$' -- openai @args
  $global:LASTEXITCODE = $LASTEXITCODE
}
` + script + `
$line = 'openai responses create ' + $env:OPENAI_CLI_COMPLETION_WORD
$expansion = TabExpansion2 $line $line.Length
$results = @($expansion.CompletionMatches | Where-Object { $_.CompletionText -ne ' ' } | ForEach-Object {
  $completed = $line.Substring(0, $expansion.ReplacementIndex) + $_.CompletionText + $line.Substring($expansion.ReplacementIndex + $expansion.ReplacementLength)
  $tokens = $null
  $errors = $null
  $ast = [System.Management.Automation.Language.Parser]::ParseInput($completed, [ref]$tokens, [ref]$errors)
  $statements = @($ast.EndBlock.Statements)
  $elements = @($statements[0].PipelineElements[0].CommandElements)
  $literal = $errors.Count -eq 0 -and $statements.Count -eq 1 -and @($statements[0].PipelineElements).Count -eq 1 -and $elements.Count -eq 4 -and $elements[-1] -is [System.Management.Automation.Language.StringConstantExpressionAst]
  [pscustomobject]@{ text = $_.CompletionText; value = $(if ($literal) { $elements[-1].Value } else { '' }); literal = $literal; container = $_.ResultType -eq 'ProviderContainer' }
})
ConvertTo-Json -InputObject $results -Compress
`
	probePath := filepath.Join(directory, "reference-probe.ps1")
	require.NoError(t, os.WriteFile(probePath, []byte(probe), 0600))
	for _, tc := range []struct{ word, prefix, parent string }{
		{"'@safe", "@", ""}, {"@file://safe", "@file://", ""}, {"@data://safe", "@data://", ""},
		{"text@safe", "text@", ""}, {"--format=@safe", "--format=@", ""},
		{"@nested/safe", "@", "nested"}, {"'@nested/safe", "@", "nested"}, {`"@nested/safe`, "@", "nested"},
		{"'@bracket[dir]/safe", "@", "bracket[dir]"}, {"'@tick`dir/safe", "@", "tick`dir"},
		{"'@apostrophe''s/safe", "@", "apostrophe's"}, {"'@missing[dir]/safe", "@", "missing[dir]"},
		{"'@file://" + filepath.Join(directory, "nested", "safe"), "@file://", filepath.Join(directory, "nested")},
	} {
		t.Run(tc.word, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, pwsh, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", probePath)
			command.Dir = directory
			command.Env = append(os.Environ(), "OPENAI_CLI_COMPLETION_HELPER=1", "OPENAI_CLI_COMPLETION_BINARY="+binary, "OPENAI_CLI_COMPLETION_WORD="+tc.word)
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			require.NoError(t, command.Run(), stderr.String())
			require.Empty(t, stderr.String())
			var results []struct {
				Text, Value        string
				Literal, Container bool
			}
			require.NoError(t, json.Unmarshal(stdout.Bytes(), &results), stdout.String())
			var values, want []string
			for _, result := range results {
				require.True(t, result.Literal, "completion must be one literal argument: %q", result.Text)
				values = append(values, result.Value)
				require.Equal(t, strings.HasSuffix(result.Value, string(os.PathSeparator)), result.Container)
			}
			if tc.parent != "missing[dir]" {
				for _, name := range append(append([]string{}, names...), "safe dir"+string(os.PathSeparator)) {
					path := filepath.Join(tc.parent, name)
					// filepath.Join removes a trailing directory separator.
					if name == "safe dir"+string(os.PathSeparator) {
						path += string(os.PathSeparator)
					}
					if tc.parent == "" {
						path = "." + string(os.PathSeparator) + path
					}
					want = append(want, tc.prefix+path)
				}
			}
			require.ElementsMatch(t, want, values)
		})
	}
}
