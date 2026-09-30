package custom

import (
	"os"
	"testing"

	"github.com/openai/openai-cli/internal/terminalimage"
)

// Match the executable dispatch when a terminal test launches its writer helper.
func TestMain(m *testing.M) {
	if handled, err := terminalimage.RunKittyOutputHelper(os.Args); handled {
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
