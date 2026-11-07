//go:build !windows

package commandsuggest

import "testing"

func TestConfigureIgnoresInvalidEnvironment(t *testing.T) {
	for _, tc := range []struct{ shell, fd string }{{"unknown", "9"}, {"zsh", "bad"}, {"zsh", "1"}, {"zsh", "99999"}} {
		t.Setenv("OPENAI_CLI_COMMANDS_SHELL", tc.shell)
		t.Setenv("OPENAI_CLI_COMMANDS_FD", tc.fd)
		Configure(nil)()
	}
}
