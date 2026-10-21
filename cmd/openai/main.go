package main

import (
	"context"
	"os"

	"github.com/openai/openai-cli/generated/commands"
	"github.com/openai/openai-cli/internal/app"
)

func main() {
	if code := app.Run(context.Background(), os.Args, commands.Command); code != 0 {
		os.Exit(code)
	}
}
