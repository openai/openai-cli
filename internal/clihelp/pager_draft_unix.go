//go:build unix

package clihelp

import (
	"os"
	"syscall"

	"github.com/openai/openai-cli/internal/terminal"
)

func openHelpDraft(in, out *os.File) *helpDraft {
	if os.Getenv("OPENAI_CLI_HELP_DRAFT_FD") != "6" || os.Getenv("OPENAI_CLI_COMMANDS_SHELL") != "zsh" || os.Getenv("OPENAI_CLI_COMMANDS_FD") != "9" {
		return nil
	}
	input, ierr := in.Stat()
	output, oerr := out.Stat()
	if ierr != nil || oerr != nil || !os.SameFile(input, output) {
		return nil
	}
	file := terminal.OpenPrivateOutput(6, "help-draft")
	if file == nil {
		return nil
	}
	// A draft and the catalog of generated command suggestions are distinct
	// transports; never publish typed text to the suggestion stream.
	suggestions := terminal.OpenPrivateOutput(9, "command-suggestions")
	if suggestions == nil {
		_ = file.Close()
		return nil
	}
	suggestionInfo, err := suggestions.Stat()
	_ = suggestions.Close()
	if err != nil {
		_ = file.Close()
		return nil
	}
	info, err := file.Stat()
	if err == nil && !os.SameFile(info, suggestionInfo) {
		if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Nlink == 0 && stat.Uid == uint32(os.Geteuid()) {
			return &helpDraft{input: &helpDraftInput{File: in, output: out}, output: file}
		}
	}
	_ = file.Close()
	return nil
}
