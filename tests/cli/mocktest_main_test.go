package cli_test

import (
	"os"
	"testing"

	"github.com/openai/openai-cli/generated/commands"
	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/pkg/custom"
)

func TestMain(m *testing.M) {
	custom.ConfigureCommand(commands.Command)
	os.Exit(mocktest.RunTests(m))
}
