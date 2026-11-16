package cli_test

import (
	"os"
	"testing"

	"github.com/openai/openai-cli/generated/commands"
	"github.com/openai/openai-cli/internal/mocktest"
	appconfig "github.com/openai/openai-cli/internal/app"
)

func TestMain(m *testing.M) {
	appconfig.ConfigureCommand(commands.Command)
	os.Exit(mocktest.RunTests(m))
}
