package custom

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestImagePickerParentShellNames(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"/opt/homebrew/bin/bash", "bash"}, {"-zsh", "zsh"}, {"fish", "fish"},
		{`C:\Program Files\PowerShell\7\pwsh.exe`, "pwsh"},
		{"/bin/sh", ""}, {"powershell.exe", ""}, {"python3", ""}, {"", ""},
	} {
		require.Equal(t, tc.want, imagePickerShellName(tc.name), tc.name)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Empty(t, imagePickerParentShell(ctx))
}
