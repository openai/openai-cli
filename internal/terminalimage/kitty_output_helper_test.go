package terminalimage

import (
	"os"
	"testing"
)

// Match the executable dispatch when a terminal test launches its writer helper.
func TestMain(m *testing.M) {
	if handled, err := RunKittyOutputHelper(os.Args); handled {
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
