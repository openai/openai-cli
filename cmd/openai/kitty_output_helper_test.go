package main

import (
	"os"
	"testing"

	"github.com/openai/openai-cli/internal/terminalimage"
)

// Match executable dispatch when tests launch private helpers.
func TestMain(m *testing.M) {
	if handled, err := terminalimage.RunKittyOutputHelper(os.Args); handled {
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
	os.Exit(m.Run())
}
