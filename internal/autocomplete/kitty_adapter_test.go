package autocomplete

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestKittySuggestionAdapterPrivateChannel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the click adapter uses Unix private FIFOs")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is required to exercise the Kitty adapter")
	}
	adapter, err := renderSuggestionAdapter("kitty")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "adapter.py")
	require.NoError(t, os.WriteFile(path, []byte(adapter), 0o600))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, python, "kitty_adapter_test.py", path, "-v").CombinedOutput()
	require.NoError(t, err, "%s", output)
	t.Logf("%s", output)
}

func TestSuggestionClickCompletionOptions(t *testing.T) {
	for _, test := range []struct {
		name, failure string
		args          []string
	}{
		{name: "adapter", args: []string{"--suggestions-adapter", "kitty"}},
		{name: "click", args: []string{"zsh", "--suggestions", "--suggestions-click"}},
		{name: "adapter shell", args: []string{"zsh", "--suggestions-adapter", "kitty"}, failure: "--suggestions-adapter cannot be combined"},
		{name: "adapter picker", args: []string{"--suggestions-adapter", "kitty", "--picker"}, failure: "--suggestions-adapter cannot be combined"},
		{name: "adapter suggestions", args: []string{"--suggestions-adapter", "kitty", "--suggestions"}, failure: "--suggestions-adapter cannot be combined"},
		{name: "adapter click", args: []string{"--suggestions-adapter", "kitty", "--suggestions-click"}, failure: "--suggestions-adapter cannot be combined"},
		{name: "adapter double tab", args: []string{"--suggestions-adapter", "kitty", "--suggestions-double-tab"}, failure: "--suggestions-adapter cannot be combined"},
		{name: "unknown adapter", args: []string{"--suggestions-adapter", "untrusted-value"}, failure: "the supported suggestion adapter is kitty"},
		{name: "click requires suggestions", args: []string{"zsh", "--suggestions-click"}, failure: "--suggestions-click requires --suggestions"},
		{name: "bash click", args: []string{"bash", "--suggestions", "--suggestions-click"}, failure: "click insertion currently supports zsh"},
		{name: "fish click", args: []string{"fish", "--suggestions", "--suggestions-click"}, failure: "click insertion currently supports zsh"},
		{name: "pwsh click", args: []string{"pwsh", "--suggestions", "--suggestions-click"}, failure: "click insertion currently supports zsh"},
		{name: "extra click argument", args: []string{"zsh", "extra", "--suggestions", "--suggestions-click"}, failure: "click insertion currently supports zsh"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			root := &cli.Command{Name: "openai", Writer: &output, ExitErrHandler: func(context.Context, *cli.Command, error) {},
				Commands: []*cli.Command{{Name: "@completion", Action: OutputCompletionScript, Flags: []cli.Flag{
					&cli.BoolFlag{Name: "picker"}, &cli.BoolFlag{Name: "suggestions"},
					&cli.BoolFlag{Name: "suggestions-double-tab"}, &cli.BoolFlag{Name: "suggestions-click"},
					&cli.StringFlag{Name: "suggestions-adapter"},
				}}}}
			err := root.Run(t.Context(), append([]string{"openai", "@completion"}, test.args...))
			if test.failure != "" {
				require.ErrorContains(t, err, test.failure)
				require.Empty(t, output.String(), "invalid combinations must not emit partial scripts")
				return
			}
			require.NoError(t, err)
			if test.name == "adapter" {
				adapter, err := renderSuggestionAdapter("kitty")
				require.NoError(t, err)
				require.Equal(t, adapter, output.String())
			} else {
				require.Contains(t, output.String(), "openai_suggestion_next")
				require.Contains(t, output.String(), "openai-click-")
				require.NotContains(t, output.String(), "__APPNAME__")
			}
		})
	}
}
