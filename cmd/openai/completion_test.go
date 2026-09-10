package main

import (
	"bytes"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInheritedCompletionMainHelper(t *testing.T) {
	if os.Getenv("OPENAI_CLI_INHERITANCE_HELPER") != "1" {
		return
	}
	os.Args = os.Args[slices.Index(os.Args, "--")+1:]
	main()
	os.Exit(0)
}

func TestInheritedCompletionMain(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)
	// Keep real credentials and environment-selected client configuration out of
	// subprocesses; completion never needs an API request or credential values.
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "OPENAI_") && !strings.HasPrefix(e, "COMPLETION_STYLE=") {
			env = append(env, e)
		}
	}
	env = append(env, "OPENAI_CLI_INHERITANCE_HELPER=1", "COMPLETION_STYLE=bash")
	for _, tc := range []struct {
		args   []string
		output string
		code   int
	}{
		{[]string{"--fo"}, "--format\n--format-error\n", 0},
		{[]string{"models", "list", "--fo"}, "--format\n--format-error\n", 0},
		{[]string{"models", "list", "--format", ""}, "", 11},
		// requestflag.Flag declares IsLocal=true, including root credential flags.
		{[]string{"models", "list", "--api-"}, "", 0},
		{[]string{"models", "--format", "list", "li"}, "list\n", 0},
		{[]string{"models", "list", "-r", "--fo"}, "--format\n--format-error\n", 0},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			args := append([]string{"-test.run=^TestInheritedCompletionMainHelper$", "--", "openai", "__complete", "--"}, tc.args...)
			child := exec.Command(binary, args...)
			child.Env = env
			var out, errs bytes.Buffer
			child.Stdout = &out
			child.Stderr = &errs
			err := child.Run()
			code := 0
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.code, code)
			require.Equal(t, tc.output, out.String())
			require.Empty(t, errs.String())
		})
	}
}
