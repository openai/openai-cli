//go:build !windows

package custom

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// pagerCommand must split a command line while keeping the configurations that work
// today: an unset PAGER, a bare program, and an executable path containing spaces.
func TestPagerCommand(t *testing.T) {
	tempDir := t.TempDir()
	pagerPath := filepath.Join(tempDir, "pager")
	require.NoError(t, os.WriteFile(pagerPath, []byte("#!/bin/sh\n"), 0700))

	spacedDir := filepath.Join(tempDir, "bin dir")
	require.NoError(t, os.Mkdir(spacedDir, 0700))
	spacedPath := filepath.Join(spacedDir, "pager")
	require.NoError(t, os.WriteFile(spacedPath, []byte("#!/bin/sh\n"), 0700))

	completePath := filepath.Join(tempDir, "pager with spaces")
	require.NoError(t, os.WriteFile(completePath, []byte("#!/bin/sh\n"), 0700))

	missingPath := filepath.Join(tempDir, "missing")

	for _, tc := range []struct {
		name  string
		pager string
		want  []string
	}{
		{name: "unset falls back to less", pager: "", want: []string{"less"}},
		{name: "blank falls back to less", pager: " \t ", want: []string{"less"}},
		{name: "bare program", pager: pagerPath, want: []string{pagerPath}},
		{name: "bare program is trimmed", pager: "  " + pagerPath + " ", want: []string{pagerPath}},
		{name: "arguments are split off", pager: pagerPath + " -R --no-init", want: []string{pagerPath, "-R", "--no-init"}},
		{name: "trimmed value with arguments", pager: " " + pagerPath + " -R", want: []string{pagerPath, "-R"}},
		{name: "path with spaces stays one program", pager: spacedPath, want: []string{spacedPath}},
		{name: "complete executable path wins over its prefix", pager: completePath, want: []string{completePath}},
		{name: "unresolvable first word keeps the whole value", pager: missingPath + " -R", want: []string{missingPath + " -R"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PAGER", tc.pager)
			require.Equal(t, tc.want, pagerCommand())
		})
	}
}

// PAGER is written as a command line by the tools this CLI is used beside
// (PAGER="less -R" is common), so both pager implementations have to pass the
// arguments through instead of resolving the whole value as one executable name.
func TestStreamToPagerPassesPagerArguments(t *testing.T) {
	paths := map[string]func(string, func(*os.File) error) error{
		"pipe":   streamToPagerWithPipe,
		"socket": streamOutputOSSpecific,
	}
	for name, stream := range paths {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			argPath, outputPath := filepath.Join(dir, "arg"), filepath.Join(dir, "output")
			pager := filepath.Join(dir, "pager")
			require.NoError(t, os.WriteFile(pager, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" > \"$OPENAI_TEST_PAGER_ARG\"\ncat > \"$OPENAI_TEST_PAGER_OUTPUT\"\n"), 0700))
			t.Setenv("PAGER", pager+" --no-init")
			t.Setenv("OPENAI_TEST_PAGER_ARG", argPath)
			t.Setenv("OPENAI_TEST_PAGER_OUTPUT", outputPath)

			require.NoError(t, stream("pager arguments", func(w *os.File) error {
				if name == "socket" {
					require.Equal(t, "parent-socket", w.Name(), "socket pager must not fall back to the pipe")
				}
				_, err := w.WriteString("payload\n")
				return err
			}))

			arg, err := os.ReadFile(argPath)
			require.NoError(t, err)
			require.Equal(t, "--no-init\n", string(arg))

			output, err := os.ReadFile(outputPath)
			require.NoError(t, err)
			require.Equal(t, "payload\n", string(output))
		})
	}
}
