package autocomplete

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

// Exercise the public backend, including adapter protocol handling. Calling
// GetCompletions directly would miss reconstruction changing the shell's words.
func wordBoundaryCompletion(t *testing.T, style CompletionStyle, words []string) (int, string) {
	t.Helper()
	t.Setenv("COMPLETION_STYLE", string(style))
	var output bytes.Buffer
	root := &cli.Command{
		Name: "openai", SkipFlagParsing: true, Writer: &output,
		Flags: []cli.Flag{&cli.StringFlag{Name: "header"}, &cli.StringFlag{Name: "file", TakesFile: true}},
		Commands: []*cli.Command{
			{Name: "responses", Commands: []*cli.Command{{Name: "create", Flags: []cli.Flag{
				&cli.StringFlag{Name: "input"}, &cli.StringFlag{Name: "model"},
			}}}},
			{Name: "chat", Commands: []*cli.Command{{Name: "completions"}}},
			{Name: "chat:completions", Hidden: true, Metadata: map[string]any{"command-compatibility-alias": true},
				Commands: []*cli.Command{{Name: "create", Flags: []cli.Flag{&cli.StringFlag{Name: "model"}}}}},
			{Name: "chat:completions:messages", Hidden: true, Metadata: map[string]any{"command-compatibility-alias": true}},
			{Name: "__complete", Hidden: true, SkipFlagParsing: true, Action: ExecuteShellCompletion},
		},
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
	}
	args := []string{"openai", "__complete"}
	switch style {
	case CompletionStyleBash, CompletionStyleFish:
		args = append(args, "--")
	case CompletionStylePowershell:
		args = append(args, "openai")
	}
	err := root.Run(t.Context(), append(args, words...))
	var exit cli.ExitCoder
	require.ErrorAs(t, err, &exit)
	return exit.ExitCode(), output.String()
}

func TestShellCompletionPreservesWordBoundaries(t *testing.T) {
	for _, style := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish, CompletionStylePowershell} {
		for _, tc := range []struct {
			name   string
			words  []string
			code   int
			output string
		}{
			{"colon input", []string{"responses", "create", "--input", ":", "--mo"}, 0, "--model\n"},
			{"repeated colon input", []string{"responses", "create", "--input", "::", "--mo"}, 0, "--model\n"},
			{"trailing colon input", []string{"responses", "create", "--input", "value:", "--mo"}, 0, "--model\n"},
			{"colon header", []string{"--header", ":", "chat", "comple"}, 0, "completions\n"},
			{"trailing colon header", []string{"--header", "chat:", "chat", "comple"}, 0, "completions\n"},
			{"colon file value", []string{"--file", ":"}, 10, ""},
			{"completed colon file", []string{"--file", ":", "chat", "comple"}, 0, "completions\n"},
			{"completed trailing colon file", []string{"--file", "assets:", "chat", "comple"}, 0, "completions\n"},
			{"nested command", []string{"chat", "comple"}, 0, "completions\n"},
			{"flag after whole alias", []string{"chat:completions", "create", "--mo"}, 0, "--model\n"},
			{"space after colon", []string{"chat:", "comple"}, 11, ""},
			{"separate colon word", []string{"chat", ":", "comple"}, 11, ""},
			{"empty word after colon", []string{"chat:", ""}, 11, ""},
		} {
			t.Run(string(style)+"/"+tc.name, func(t *testing.T) {
				t.Setenv("OPENAI_CLI_COMPLETION_PRESERVE_WORDS", "1")
				code, output := wordBoundaryCompletion(t, style, tc.words)
				require.Equal(t, tc.code, code)
				require.Equal(t, tc.output, output)
			})
		}
		for _, tc := range []struct {
			word, bash, other string
		}{
			{"chat:", "completions\ncompletions:messages\n", "chat:completions\nchat:completions:messages\n"},
			{"chat:comple", "completions\ncompletions:messages\n", "chat:completions\nchat:completions:messages\n"},
			{"chat:completions:mess", "messages\n", "chat:completions:messages\n"},
		} {
			t.Run(string(style)+"/"+tc.word, func(t *testing.T) {
				t.Setenv("OPENAI_CLI_COMPLETION_PRESERVE_WORDS", "1")
				code, output := wordBoundaryCompletion(t, style, []string{tc.word})
				require.Zero(t, code)
				want := tc.other
				if style == CompletionStyleBash {
					want = tc.bash
				} else if style == CompletionStyleZsh {
					want = strings.ReplaceAll(want, ":", `\:`)
				}
				require.Equal(t, want, output)
			})
		}
	}
}

func TestShellCompletionLegacySplitWords(t *testing.T) {
	// Previously loaded adapters still send colon-separated fragments. Only an
	// exact capability value opts into treating each backend argument as a word.
	for _, capability := range []string{"unset", "", "0", "true", "other-adapter"} {
		t.Run(capability, func(t *testing.T) {
			t.Setenv("OPENAI_CLI_COMPLETION_PRESERVE_WORDS", capability)
			if capability == "unset" {
				require.NoError(t, os.Unsetenv("OPENAI_CLI_COMPLETION_PRESERVE_WORDS"))
			}
			for _, tc := range []struct {
				words  []string
				output string
			}{
				{[]string{"chat", ":", "comple"}, "completions\ncompletions:messages\n"},
				{[]string{"chat", ":", "completions", ":", "mess"}, "messages\n"},
				{[]string{"chat", ":", "completions", "create", "--mo"}, "--model\n"},
			} {
				code, output := wordBoundaryCompletion(t, CompletionStyleBash, tc.words)
				require.Zero(t, code, "%q", tc.words)
				require.Equal(t, tc.output, output, "%q", tc.words)
			}
		})
	}
}
