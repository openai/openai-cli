package main

import (
	"context"
	"os"

	"github.com/openai/openai-cli/internal/app"
)

func main() {
	if code := app.Run(context.Background(), os.Args); code != 0 {
		os.Exit(code)
	}
}
