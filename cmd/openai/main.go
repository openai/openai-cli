package main

import (
	"os"

	"github.com/openai/openai-cli/pkg/cmd"
	"github.com/openai/openai-cli/pkg/custom"
)

func main() {
	os.Exit(custom.Run(cmd.Command, os.Args))
}
