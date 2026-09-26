package custom

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/internal/terminalimage"
	"github.com/urfave/cli/v3"
)

const imagePreviewSetupHelp = `{{$bin := or (index .Root.Metadata "help-invocation") "openai"}}{{.Usage}}
  {{$bin}} images inline {{.Name}}

Local Apple Terminal on macOS only; no SSH or terminal multiplexers.
Reading the tab may request macOS Automation permission, including status.
Status is read-only; it does not register fonts or change settings.
Setup and repair keep the text font, size, profile and cached image mappings.
They add no sample image. Automatic preview preferences are unchanged.
Use --inline on when generating an image for sharp Apple Terminal previews.

Missing selected font: select your original font and size, then run repair.
Missing metadata or thumbnails: retain the files and use a new Terminal tab.
No cache reset or removal of committed previews is performed.
No API requests. Readable output only: --format auto or text.

Full help: {{$bin}} help --all images inline {{.Name}}
`

func registerImagePreviewSetup(root *cli.Command) {
	images := root.Command("images")
	if images == nil {
		return
	}
	inline := images.Command("inline")
	if inline == nil {
		inline = &cli.Command{Name: "inline", Usage: "Manage image previews in this terminal", HideHelpCommand: true}
		images.Commands = append(images.Commands, inline)
	}
	for _, item := range []struct {
		name, usage string
		run         func(context.Context, io.Writer) error
	}{
		{"setup", "Prepare this Apple Terminal tab for sharp previews", terminalimage.SetupFont},
		{"repair", "Restore this tab's cached previews after a restart or font change", terminalimage.RepairFont},
		{"status", "Check this tab and its preview cache without changing settings", terminalimage.FontStatus},
	} {
		inline.Commands = append(inline.Commands, &cli.Command{
			Name: item.name, Usage: item.usage, HideHelpCommand: true,
			CustomHelpTemplate: imagePreviewSetupHelp,
			Description:        "For a local Apple Terminal tab on macOS. Reading this tab may request Automation permission. Setup and repair preserve the selected text font, size and profile, retain cached images and add no sample image. Use --inline on when generating an image; automatic preview preferences are unchanged. Status reads the tab and cache without registering fonts or changing settings. Missing cache metadata cannot restore old scrollback; use a new tab. No cache reset or removal of committed previews is performed.",
			Action:             imagePreviewSetupAction(item.run),
		})
	}
}

func imagePreviewSetupAction(run func(context.Context, io.Writer) error) cli.ActionFunc {
	return func(ctx context.Context, command *cli.Command) error {
		if command.Args().Present() {
			return imageSavingFailure("This local image command takes no arguments; use --help for examples.", nil)
		}
		root := command.Root()
		if format := strings.ToLower(root.String("format")); format != "" && format != "auto" && format != "text" {
			return imageSavingFailure("This local image command uses readable output; use --format auto or text.", nil)
		}
		if root.String("transform") != "" || root.Bool("raw-output") {
			return imageSavingFailure("This local image command cannot use --transform or --raw-output.", nil)
		}
		out := root.Writer
		if out == nil {
			out = os.Stdout
		}
		interruptContext, stop := signal.NotifyContext(ctx, os.Interrupt)
		defer stop()
		if err := run(interruptContext, out); err != nil {
			message := err.Error()
			var pathErr *os.PathError
			var linkErr *os.LinkError
			if errors.As(err, &pathErr) || errors.As(err, &linkErr) {
				// Wrapper and joined errors can contain multiple private cache
				// paths. Keep the cause for callers, but no path in diagnostics.
				message = "Preview cache files could not be accessed. Retain the cache and saved originals; use a new Terminal tab if files are missing or damaged."
			}
			return imageSavingFailure(readable.Text(message), err)
		}
		return nil
	}
}
