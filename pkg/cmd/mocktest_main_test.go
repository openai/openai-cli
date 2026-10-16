package cmd

import (
	"os"
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestMain(m *testing.M) {
	os.Exit(mocktest.RunTests(m))
}
