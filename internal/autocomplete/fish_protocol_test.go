package autocomplete

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestFishFileCompletionPreservesAssignments(t *testing.T) {
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("Fish is not available")
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	script, err := shellCompletions[CompletionStyleFish](&cli.Command{}, "openai")
	require.NoError(t, err)
	dir := t.TempDir()
	for _, name := range []string{"assets/logo.png", "assets/long name.png", "assets/a=b.png", "assets/l[bracket].png", "--file=logo.png"} {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		require.NoError(t, os.WriteFile(path, nil, 0600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "openai"), []byte("#!/bin/sh\nexec \"$OPENAI_CLI_COMPLETION_BINARY\" '-test.run=^TestShellCompletionProtocolHelper$' -- openai \"$@\"\n"), 0700))
	probe := script + `
complete -C "$OPENAI_CLI_COMPLETION_LINE"
`
	for _, tc := range []struct {
		line string
		want []string
	}{
		{"openai --file assets/lo", []string{"assets/logo.png", "assets/long name.png"}},
		{"openai --file=assets/lo", []string{"--file=assets/logo.png", "--file=assets/long name.png"}},
		{"openai --image=assets/lo", []string{"--image=assets/logo.png", "--image=assets/long name.png"}},
		{"openai --file=assets/a=", []string{"--file=assets/a=b.png"}},
		{"openai --file='assets/long ", []string{"--file=assets/long name.png"}},
		{"openai --file=assets/l[", []string{"--file=assets/l[bracket].png"}},
		{"openai --file --file=lo", []string{"--file=logo.png"}},
		{"openai --format=assets/lo", nil},
		{"openai --missing=assets/lo", nil},
		{"openai -- --file=assets/lo", nil},
	} {
		t.Run(tc.line, func(t *testing.T) {
			command := exec.Command(fish, "--no-config", "-c", probe)
			command.Dir = dir
			command.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "OPENAI_CLI_COMPLETION_HELPER=1", "OPENAI_CLI_COMPLETION_BINARY="+binary, "OPENAI_CLI_COMPLETION_LINE="+tc.line)
			out, err := command.CombinedOutput()
			require.NoError(t, err, string(out))
			var got []string
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if line != "" {
					name, _, _ := strings.Cut(line, "\t")
					got = append(got, name)
				}
			}
			require.ElementsMatch(t, tc.want, got)
		})
	}
}
