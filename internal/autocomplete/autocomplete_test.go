package autocomplete

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/urfave/cli/v3"
)

func TestGetCompletions_EmptyArgs(t *testing.T) {
	t.Parallel()

	root := &cli.Command{
		Commands: []*cli.Command{
			{Name: "generate", Usage: "Generate SDK"},
			{Name: "test", Usage: "Run tests"},
			{Name: "build", Usage: "Build project"},
		},
	}

	result := GetCompletions(CompletionStyleBash, root, []string{})

	assert.Equal(t, ShellCompletionBehaviorDefault, result.Behavior)
	assert.Len(t, result.Completions, 3)
	assert.Contains(t, result.Completions, ShellCompletion{Name: "generate", Usage: "Generate SDK"})
	assert.Contains(t, result.Completions, ShellCompletion{Name: "test", Usage: "Run tests"})
	assert.Contains(t, result.Completions, ShellCompletion{Name: "build", Usage: "Build project"})
}

func TestGetCompletions_SubcommandPrefix(t *testing.T) {
	t.Parallel()

	root := &cli.Command{
		Commands: []*cli.Command{
			{Name: "generate", Usage: "Generate SDK"},
			{Name: "test", Usage: "Run tests"},
			{Name: "build", Usage: "Build project"},
		},
	}

	result := GetCompletions(CompletionStyleBash, root, []string{"ge"})

	assert.Equal(t, ShellCompletionBehaviorDefault, result.Behavior)
	assert.Len(t, result.Completions, 1)
	assert.Equal(t, "generate", result.Completions[0].Name)
	assert.Equal(t, "Generate SDK", result.Completions[0].Usage)
}

func TestGetCompletions_HiddenCommand(t *testing.T) {
	t.Parallel()

	root := &cli.Command{
		Commands: []*cli.Command{
			{Name: "visible", Usage: "Visible command"},
			{Name: "hidden", Usage: "Hidden command", Hidden: true},
		},
	}

	result := GetCompletions(CompletionStyleBash, root, []string{""})

	assert.Len(t, result.Completions, 1)
	assert.Equal(t, "visible", result.Completions[0].Name)
}

func TestGetCompletions_NestedSubcommand(t *testing.T) {
	t.Parallel()

	root := &cli.Command{
		Commands: []*cli.Command{
			{
				Name:  "config",
				Usage: "Configuration commands",
				Commands: []*cli.Command{
					{Name: "get", Usage: "Get config value"},
					{Name: "set", Usage: "Set config value"},
				},
			},
		},
	}

	result := GetCompletions(CompletionStyleBash, root, []string{"config", "s"})

	assert.Equal(t, ShellCompletionBehaviorDefault, result.Behavior)
	assert.Len(t, result.Completions, 1)
	assert.Equal(t, "set", result.Completions[0].Name)
	assert.Equal(t, "Set config value", result.Completions[0].Usage)
}

func TestGetCompletions_FlagCompletion(t *testing.T) {
	t.Parallel()

	root := &cli.Command{
		Commands: []*cli.Command{
			{
				Name:  "generate",
				Usage: "Generate SDK",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "Output directory"},
					&cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}, Usage: "Verbose output"},
					&cli.StringFlag{Name: "format", Usage: "Output format"},
				},
			},
		},
	}

	result := GetCompletions(CompletionStyleBash, root, []string{"generate", "--o"})

	assert.Equal(t, ShellCompletionBehaviorDefault, result.Behavior)
	assert.Len(t, result.Completions, 1)
	assert.Equal(t, "--output", result.Completions[0].Name)
	assert.Equal(t, "Output directory", result.Completions[0].Usage)
}

func TestGetCompletions_ShortFlagCompletion(t *testing.T) {
	t.Parallel()

	root := &cli.Command{
		Commands: []*cli.Command{
			{
				Name:  "generate",
				Usage: "Generate SDK",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "Output directory"},
					&cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}, Usage: "Verbose output"},
				},
			},
		},
	}

	result := GetCompletions(CompletionStyleBash, root, []string{"generate", "-v"})

	assert.Equal(t, ShellCompletionBehaviorDefault, result.Behavior)
	assert.Len(t, result.Completions, 1)
	assert.Equal(t, "-v", result.Completions[0].Name)
}

func TestGetCompletions_FileFlagBehavior(t *testing.T) {
	t.Parallel()

	root := &cli.Command{
		Commands: []*cli.Command{
			{
				Name:  "generate",
				Usage: "Generate SDK",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "config", Aliases: []string{"c"}, Usage: "Config file", TakesFile: true},
				},
			},
		},
	}

	result := GetCompletions(CompletionStyleBash, root, []string{"generate", "--config", ""})

	assert.EqualValues(t, ShellCompletionBehaviorFile, result.Behavior)
	assert.Empty(t, result.Completions)
}

func TestBashCompletionFileCandidatesUseFilenameQuoting(t *testing.T) {
	t.Parallel()

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not available")
	}

	completionScript, err := shellCompletions[CompletionStyleBash](&cli.Command{}, "openai")
	if !assert.NoError(t, err) {
		return
	}

	probe := `
cd "$1" || exit 1
touch '$(printf autocomplete-marker > completion-output)'

if ! type mapfile >/dev/null 2>&1; then
  mapfile() {
    if [[ "$1" == "-t" ]]; then
      shift
    fi
    if [[ "$1" != "COMPREPLY" ]]; then
      return 2
    fi

    COMPREPLY=()
    local line
    while IFS= read -r line; do
      COMPREPLY+=("$line")
    done
  }
fi

openai() {
  return 10
}

` + completionScript + `

printf 'spec:%s\n' "$(complete -p openai)"

COMP_WORDS=(openai files create --file '@$(')
COMP_CWORD=4
__openai_bash_autocomplete
printf 'forced:%s\n' "${COMPREPLY[0]}"

COMP_WORDS=(openai upload '$(')
COMP_CWORD=2
__openai_bash_autocomplete
printf 'file:%s\n' "${COMPREPLY[0]}"

if [[ -e completion-output ]]; then
  printf 'unexpected completion-output file\n'
  exit 1
fi
`

	cmd := exec.Command(bash, "-c", probe, "bash-completion-probe", t.TempDir())
	out, err := cmd.CombinedOutput()
	output := string(out)

	if !assert.NoError(t, err, output) {
		return
	}
	assert.Contains(t, output, "spec:complete -o filenames -F __openai_bash_autocomplete openai")
	assert.Contains(t, output, "forced:@$(printf autocomplete-marker > completion-output)")
	assert.Contains(t, output, "file:$(printf autocomplete-marker > completion-output)")
	assert.NotContains(t, output, "unexpected completion-output file")
}

func TestBashCompletionScriptDoesNotRegisterPlainCompletion(t *testing.T) {
	t.Parallel()

	completionScript, err := shellCompletions[CompletionStyleBash](&cli.Command{}, "openai")
	if !assert.NoError(t, err) {
		return
	}

	assert.Contains(t, completionScript, "complete -o filenames -F __openai_bash_autocomplete openai")
	assert.False(t, strings.Contains(completionScript, "\ncomplete -F __openai_bash_autocomplete openai"))
}

func TestFishCompletionScriptDiscardsDiagnostics(t *testing.T) {
	t.Parallel()

	completionScript, err := shellCompletions[CompletionStyleFish](&cli.Command{}, "openai")
	if !assert.NoError(t, err) {
		return
	}

	assert.Contains(t, completionScript, "$current 2>/dev/null)")
	assert.NotContains(t, completionScript, "/tmp/fish-debug.log")
	assert.Contains(t, completionScript, "complete -c openai -f -a '(__openai_fish_autocomplete)'")
}

func TestFishCompletionDoesNotWriteDiagnostics(t *testing.T) {
	t.Parallel()

	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish is not available")
	}

	completionScript, err := shellCompletions[CompletionStyleFish](&cli.Command{}, "openai")
	if !assert.NoError(t, err) {
		return
	}

	tests := []struct {
		name           string
		baseURL        string
		prepareLog     func(t *testing.T, debugLog string) string
		wantCompletion bool
	}{
		{
			name:           "successful completion does not create a diagnostic file",
			baseURL:        "https://example.invalid",
			wantCompletion: true,
		},
		{
			name:    "malformed endpoint does not create a diagnostic file",
			baseURL: "malformed-user:synthetic-secret@example.invalid",
		},
		{
			name:    "malformed endpoint does not append to an existing diagnostic file",
			baseURL: "malformed://synthetic-user:synthetic-secret@internal.invalid",
			prepareLog: func(t *testing.T, debugLog string) string {
				t.Helper()
				if !assert.NoError(t, os.WriteFile(debugLog, []byte("unchanged\n"), 0o600)) {
					return ""
				}
				return debugLog
			},
		},
		{
			name:    "malformed endpoint does not follow a preexisting symlink",
			baseURL: "malformed://synthetic-user:synthetic-secret@internal.invalid",
			prepareLog: func(t *testing.T, debugLog string) string {
				t.Helper()
				target := filepath.Join(filepath.Dir(debugLog), "protected-target")
				if !assert.NoError(t, os.WriteFile(target, []byte("unchanged\n"), 0o600)) {
					return ""
				}
				if !assert.NoError(t, os.Symlink(target, debugLog)) {
					return ""
				}
				return target
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			debugLog := filepath.Join(dir, "fish-debug.log")
			protectedFile := ""
			if test.prepareLog != nil {
				protectedFile = test.prepareLog(t, debugLog)
				if protectedFile == "" {
					return
				}
			}

			mockCLI := `#!/bin/sh
case "$OPENAI_BASE_URL" in
  malformed*)
    printf 'OPENAI_BASE_URL "%s" is missing a scheme (expected http:// or https://)\n' "$OPENAI_BASE_URL" >&2
    exit 1
    ;;
esac
printf 'models\tList models\n'
printf 'moderations\tCreate moderations\n'
`
			if !assert.NoError(t, os.WriteFile(filepath.Join(dir, "openai"), []byte(mockCLI), 0o755)) {
				return
			}

			scriptPath := filepath.Join(dir, "completion.fish")
			isolatedScript := strings.ReplaceAll(completionScript, "/tmp/fish-debug.log", debugLog)
			if !assert.NoError(t, os.WriteFile(scriptPath, []byte(isolatedScript), 0o600)) {
				return
			}

			cmd := exec.Command(fish, "--no-config", "-c", `source $argv[1]; complete -C "openai mo"`, scriptPath)
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "OPENAI_BASE_URL="+test.baseURL)
			out, err := cmd.CombinedOutput()
			output := string(out)
			if !assert.NoError(t, err, output) {
				return
			}

			if test.wantCompletion {
				assert.Contains(t, output, "models\tList models")
				assert.Contains(t, output, "moderations\tCreate moderations")
			} else {
				assert.Empty(t, output)
			}
			assert.NotContains(t, output, "synthetic-secret")

			if protectedFile == "" {
				_, err := os.Lstat(debugLog)
				assert.True(t, os.IsNotExist(err), "unexpected diagnostic file %q", debugLog)
				return
			}

			contents, err := os.ReadFile(protectedFile)
			if assert.NoError(t, err) {
				assert.Equal(t, "unchanged\n", string(contents))
			}
		})
	}
}

func TestGetCompletions_NonBoolFlagValue(t *testing.T) {
	t.Parallel()

	root := &cli.Command{
		Commands: []*cli.Command{
			{
				Name:  "generate",
				Usage: "Generate SDK",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "format", Usage: "Output format"},
				},
			},
		},
	}

	result := GetCompletions(CompletionStyleBash, root, []string{"generate", "--format", ""})

	assert.EqualValues(t, ShellCompletionBehaviorNoComplete, result.Behavior)
	assert.Empty(t, result.Completions)
}

func TestGetCompletions_BoolFlagDoesNotBlockCompletion(t *testing.T) {
	t.Parallel()

	root := &cli.Command{
		Commands: []*cli.Command{
			{
				Name:  "generate",
				Usage: "Generate SDK",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}, Usage: "Verbose output"},
				},
				Commands: []*cli.Command{
					{Name: "typescript", Usage: "Generate TypeScript SDK"},
					{Name: "python", Usage: "Generate Python SDK"},
				},
			},
		},
	}

	result := GetCompletions(CompletionStyleBash, root, []string{"generate", "--verbose", "ty"})

	assert.Equal(t, ShellCompletionBehaviorDefault, result.Behavior)
	assert.Len(t, result.Completions, 1)
	assert.Equal(t, "typescript", result.Completions[0].Name)
}

func TestGetCompletions_ColonCommands_NoColonTyped(t *testing.T) {
	t.Parallel()

	root := &cli.Command{
		Commands: []*cli.Command{
			{Name: "config:get", Usage: "Get config value"},
			{Name: "config:set", Usage: "Set config value"},
			{Name: "config:list", Usage: "List config values"},
		},
	}

	result := GetCompletions(CompletionStyleBash, root, []string{"co"})

	// Should collapse to single "config" entry without usage
	assert.Len(t, result.Completions, 1)
	assert.Equal(t, "config", result.Completions[0].Name)
	assert.Equal(t, "", result.Completions[0].Usage)
}

func TestGetCompletions_ColonCommands_ColonTyped_Bash(t *testing.T) {
	t.Parallel()

	root := &cli.Command{
		Commands: []*cli.Command{
			{Name: "config:get", Usage: "Get config value"},
			{Name: "config:set", Usage: "Set config value"},
			{Name: "config:list", Usage: "List config values"},
		},
	}

	result := GetCompletions(CompletionStyleBash, root, []string{"config:"})

	// For bash, should show suffixes only
	assert.Len(t, result.Completions, 3)
	names := []string{result.Completions[0].Name, result.Completions[1].Name, result.Completions[2].Name}
	assert.Contains(t, names, "get")
	assert.Contains(t, names, "set")
	assert.Contains(t, names, "list")
}

func TestGetCompletions_ColonCommands_ColonTyped_Zsh(t *testing.T) {
	t.Parallel()

	root := &cli.Command{
		Commands: []*cli.Command{
			{Name: "config:get", Usage: "Get config value"},
			{Name: "config:set", Usage: "Set config value"},
			{Name: "config:list", Usage: "List config values"},
		},
	}

	result := GetCompletions(CompletionStyleZsh, root, []string{"config:"})

	// For zsh, should show full names
	assert.Len(t, result.Completions, 3)
	names := []string{result.Completions[0].Name, result.Completions[1].Name, result.Completions[2].Name}
	assert.Contains(t, names, "config:get")
	assert.Contains(t, names, "config:set")
	assert.Contains(t, names, "config:list")
}

func TestGetCompletions_BashStyleColonCompletion(t *testing.T) {
	t.Parallel()

	root := &cli.Command{
		Commands: []*cli.Command{
			{Name: "config:get", Usage: "Get config value"},
			{Name: "config:set", Usage: "Set config value"},
		},
	}

	result := GetCompletions(CompletionStyleBash, root, []string{"config:g"})

	// For bash, should return suffix from after the colon in the input
	// Input "config:g" has colon at index 6, so we take name[7:] from matched commands
	assert.Len(t, result.Completions, 1)
	assert.Equal(t, "get", result.Completions[0].Name)
	assert.Equal(t, "Get config value", result.Completions[0].Usage)
}

func TestGetCompletions_BashStyleColonCompletion_NoMatch(t *testing.T) {
	t.Parallel()

	root := &cli.Command{
		Commands: []*cli.Command{
			{Name: "config:get", Usage: "Get config value"},
			{Name: "config:set", Usage: "Set config value"},
		},
	}

	result := GetCompletions(CompletionStyleBash, root, []string{"other:g"})

	// No matches
	assert.Len(t, result.Completions, 0)
}

func TestGetCompletions_ZshStyleColonCompletion(t *testing.T) {
	t.Parallel()

	root := &cli.Command{
		Commands: []*cli.Command{
			{Name: "config:get", Usage: "Get config value"},
			{Name: "config:set", Usage: "Set config value"},
		},
	}

	result := GetCompletions(CompletionStyleZsh, root, []string{"config:g"})

	// For zsh, should return full name
	assert.Len(t, result.Completions, 1)
	assert.Equal(t, "config:get", result.Completions[0].Name)
	assert.Equal(t, "Get config value", result.Completions[0].Usage)
}

func TestGetCompletions_MixedColonAndRegularCommands(t *testing.T) {
	t.Parallel()

	root := &cli.Command{
		Commands: []*cli.Command{
			{Name: "generate", Usage: "Generate SDK"},
			{Name: "config:get", Usage: "Get config value"},
			{Name: "config:set", Usage: "Set config value"},
		},
	}

	result := GetCompletions(CompletionStyleBash, root, []string{""})

	// Should show "generate" and "config" (collapsed)
	assert.Len(t, result.Completions, 2)
	names := []string{result.Completions[0].Name, result.Completions[1].Name}
	assert.Contains(t, names, "generate")
	assert.Contains(t, names, "config")
}

func TestGetCompletions_FlagWithBoolFlagSkipsValue(t *testing.T) {
	t.Parallel()

	root := &cli.Command{
		Commands: []*cli.Command{
			{
				Name:  "generate",
				Usage: "Generate SDK",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}},
					&cli.StringFlag{Name: "output", Aliases: []string{"o"}},
				},
				Commands: []*cli.Command{
					{Name: "typescript", Usage: "TypeScript SDK"},
				},
			},
		},
	}

	// Bool flag should not consume the next arg as a value
	result := GetCompletions(CompletionStyleBash, root, []string{"generate", "-v", "ty"})

	assert.Len(t, result.Completions, 1)
	assert.Equal(t, "typescript", result.Completions[0].Name)
}

func TestGetCompletions_MultipleFlagsBeforeSubcommand(t *testing.T) {
	t.Parallel()

	root := &cli.Command{
		Commands: []*cli.Command{
			{
				Name:  "generate",
				Usage: "Generate SDK",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "config", Aliases: []string{"c"}},
					&cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}},
				},
				Commands: []*cli.Command{
					{Name: "typescript", Usage: "TypeScript SDK"},
					{Name: "python", Usage: "Python SDK"},
				},
			},
		},
	}

	result := GetCompletions(CompletionStyleBash, root, []string{"generate", "-c", "config.yml", "-v", "py"})

	assert.Len(t, result.Completions, 1)
	assert.Equal(t, "python", result.Completions[0].Name)
}

func TestGetCompletions_CommandAliases(t *testing.T) {
	t.Parallel()

	root := &cli.Command{
		Commands: []*cli.Command{
			{Name: "generate", Aliases: []string{"gen", "g"}, Usage: "Generate SDK"},
		},
	}

	result := GetCompletions(CompletionStyleBash, root, []string{"g"})

	// Should match all aliases that start with "g"
	assert.GreaterOrEqual(t, len(result.Completions), 2) // "generate" and "gen", possibly "g" too
	names := []string{}
	for _, c := range result.Completions {
		names = append(names, c.Name)
	}
	assert.Contains(t, names, "generate")
	assert.Contains(t, names, "gen")
}

func TestGetCompletions_AllFlagsWhenNoPrefix(t *testing.T) {
	t.Parallel()

	root := &cli.Command{
		Commands: []*cli.Command{
			{
				Name:  "generate",
				Usage: "Generate SDK",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "output", Aliases: []string{"o"}},
					&cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}},
					&cli.StringFlag{Name: "format", Aliases: []string{"f"}},
				},
			},
		},
	}

	result := GetCompletions(CompletionStyleBash, root, []string{"generate", "-"})

	// Should show all flag variations
	assert.GreaterOrEqual(t, len(result.Completions), 6) // -o, --output, -v, --verbose, -f, --format
}
