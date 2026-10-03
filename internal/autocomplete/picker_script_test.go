package autocomplete

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestPickerScriptRequiresSafeCommandName(t *testing.T) {
	for _, name := range []string{"", "openai;echo bad", "../openai", "x'\n", "openai.exe", "$(id)", "雪", "1name"} {
		script, err := renderPickerCompletion(CompletionStyleZsh, name)
		require.Error(t, err, name)
		require.Empty(t, script)
	}
	_, err := renderPickerCompletion("unknown", "openai")
	require.Error(t, err)
}

func TestPickerCompletionRequiresExplicitOptIn(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "pwsh"} {
		t.Run(shell, func(t *testing.T) {
			for _, enabled := range []bool{false, true} {
				var output bytes.Buffer
				root := &cli.Command{Name: "openai", Writer: &output, Commands: []*cli.Command{{
					Name: "@completion", Flags: []cli.Flag{&cli.BoolFlag{Name: "picker"}}, Action: OutputCompletionScript,
				}}, ExitErrHandler: func(context.Context, *cli.Command, error) {}}
				args := []string{"openai", "@completion", shell}
				if enabled {
					args = append(args, "--picker")
				}
				err := root.Run(context.Background(), args)
				if shell == "pwsh" && enabled {
					require.EqualError(t, err, "PowerShell uses normal Tab completion. Type openai images generate and press Enter to open the image picker.")
					var exit cli.ExitCoder
					require.ErrorAs(t, err, &exit)
					require.Equal(t, 1, exit.ExitCode())
					require.Empty(t, output.String(), "unsupported setup must not emit an executable partial script")
					continue
				}
				require.NoError(t, err)
				standard, err := shellCompletions[CompletionStyle(shell)](root, "openai")
				require.NoError(t, err)
				if enabled {
					hook, err := renderPickerCompletion(CompletionStyle(shell), "openai")
					require.NoError(t, err)
					require.Equal(t, standard+"\n"+hook, output.String())
					require.Contains(t, output.String(), "openai_picker_disable")
					require.NotContains(t, output.String(), "__APPNAME__")
				} else {
					require.Equal(t, standard, output.String())
				}
			}
		})
	}
}

func TestPickerScriptFailureDoesNotEmitPartialSetup(t *testing.T) {
	var output bytes.Buffer
	root := &cli.Command{Name: "unsafe;name", Writer: &output, Commands: []*cli.Command{{
		Name: "@completion", Flags: []cli.Flag{&cli.BoolFlag{Name: "picker"}}, Action: OutputCompletionScript,
	}}, ExitErrHandler: func(context.Context, *cli.Command, error) {}}
	err := root.Run(context.Background(), []string{"unsafe;name", "@completion", "bash", "--picker"})
	require.Error(t, err)
	require.Empty(t, output.String())
}
