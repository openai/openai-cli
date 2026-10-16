package cli_test

import (
	"github.com/openai/openai-cli/generated/commands"
	"github.com/openai/openai-cli/pkg/custom"
	"os"
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestMain(m *testing.M) {
	custom.ConfigureCommand(commands.Command)
	os.Exit(mocktest.RunTests(m))
}
