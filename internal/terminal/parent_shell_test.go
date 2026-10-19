package terminal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImagePickerParentShellNames(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"/opt/homebrew/bin/bash", "bash"}, {"-zsh", "zsh"}, {"fish", "fish"},
		{`C:\Program Files\PowerShell\7\pwsh.exe`, "pwsh"},
		{"/bin/sh", ""}, {"powershell.exe", ""}, {"python3", ""}, {"", ""},
	} {
		require.Equal(t, tc.want, ShellName(tc.name), tc.name)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Empty(t, ParentShell(ctx))
}

func TestImagePickerDetectedShellCommand(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"bash", "bash"}, {"-zsh", "zsh"}, {"/usr/bin/fish", "fish"},
		{`C:\Program Files\PowerShell\7\pwsh.exe`, "pwsh"},
		{"powershell.exe", ""}, {"/bin/sh", ""}, {"python3", ""}, {"", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shell := ShellName(tc.name)
			command := FormatCommand([]string{"images", "generate", "--prompt", "a cat's portrait"}, shell)
			if tc.want == "" {
				require.Empty(t, command, "an unknown detector result must not select Bash")
			} else {
				require.NotEmpty(t, command)
				require.Equal(t, FormatCommand([]string{"images", "generate", "--prompt", "a cat's portrait"}, tc.want), command)
			}
		})
	}
}
