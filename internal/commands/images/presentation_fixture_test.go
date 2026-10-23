package images

import (
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/terminal"
)

type outputJSON = clioutput.JSON

var isTerminal = terminal.IsWriterTerminal
