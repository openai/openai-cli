package main

import (
	"os"
	"testing"

	"github.com/openai/openai-cli/generated/commands"
	"github.com/openai/openai-cli/internal/terminalimage"
	"github.com/openai/openai-cli/pkg/custom"
)

// Match executable dispatch when tests launch private helpers.
func TestMain(m *testing.M) {
	if handled, err := terminalimage.RunKittyOutputHelper(os.Args); handled {
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if handled, err := custom.RunBatchOutputHelper(os.Args); handled {
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if runBatchDirectoryFixture(os.Args) {
		os.Exit(0)
	}
	if handled, err := custom.RunBatchDirectoryHelper(os.Args); handled {
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if handled, err := runAudioWorkbenchHelper(); handled {
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	custom.ConfigureCommand(commands.Command)
	os.Exit(runShellTests(m))
}
