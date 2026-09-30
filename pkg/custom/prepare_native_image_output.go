package custom

import (
	"context"
	"os"
	"strings"

	"github.com/openai/openai-cli/internal/terminalimage"
	"github.com/urfave/cli/v3"
)

// Bind before stdin or uploads can block. Body-selected URL output may make the
// session unnecessary; its unused startup/close errors must not affect API data.
func prepareNativeImageOutput(ctx context.Context, command *cli.Command) (context.Context, func() error) {
	skip := func() (context.Context, func() error) { return ctx, func() error { return nil } }
	root := command.Root()
	format := strings.ToLower(root.String("format"))
	if (format != "" && format != "auto" && format != "text") || root.String("transform") != "" || root.Bool("raw-output") {
		return skip()
	}
	if response, ok := command.Value("response-format").(*string); ok && command.IsSet("response-format") && response != nil && *response == "url" {
		return skip()
	}
	out := imageCommandWriter(command)
	if savedImageProtocol(command.String("inline"), isTerminal(out), os.Getenv) != "kitty" {
		return skip()
	}
	return terminalimage.PrepareKittyOutput(ctx, out)
}
