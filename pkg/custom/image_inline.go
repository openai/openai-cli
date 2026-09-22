package custom

import (
	"context"
	"errors"
	"fmt"

	"github.com/openai/openai-cli/internal/terminalimage"
	"github.com/urfave/cli/v3"
)

// Apple Terminal's opt-in bitmap-font renderer uses the current tab's text and
// profile settings. Setup never creates, imports, or selects another profile.
func registerImageInline(root *cli.Command) {
	for _, resource := range root.Commands {
		if resource.Name != "images" {
			continue
		}
		resource.Commands = append(resource.Commands, &cli.Command{
			Name: "inline", Usage: "Manage image previews and Apple Terminal setup.",
			Description: "Remember automatic previews with on/off on any platform.\nApple Terminal setup enables the image font in your current tab, keeping your selected profile and colors (experimental). No API calls.\nmacOS may ask for Terminal automation permission.\nUse --inline on or --inline off to override your preference for one generation.",
			Commands: append([]*cli.Command{
				{Name: "setup", Usage: "Enable sharp previews in this Apple Terminal tab.",
					Description: "Keeps your text style, font size, Inspector profile, colors and background.\nAdds image glyphs to a private local copy of your installed font; source fonts are unchanged.\nShows a local sample without opening another window or making an API call.\nIf upgrading an older preview font, select your preferred font and size in Inspector first.",
					Action:      setupImageInline},
				{Name: "test", Usage: "Check this window and show a sample image without an API call.", Action: testImageInline},
				{Name: "repair", Usage: "Repair a missing preview font and enable this tab.",
					Action: setupImageInline},
				{Name: "status", Usage: "Show the local image gallery and its capacity.",
					Flags: []cli.Flag{&cli.BoolFlag{Name: "check", Usage: "Check this tab's image font, font size and window width"}, &cli.BoolFlag{Name: "details", Usage: "Show cache path and exact preview capacity"}}, Action: statusImageInline},
				{Name: "reset", Usage: "Clear cached previews after closing tabs using image fonts.",
					Description: "Removes local preview fonts and thumbnails. Old image scrollback will no longer display.\nOriginal saved image files are kept. Run setup afterward to start a new gallery.", Action: resetImageInline},
			}, imageInlinePreferenceCommands()...),
		})
	}
}

func checkImageInlineOutput(cmd *cli.Command) error {
	if cmd.Args().Len() != 0 {
		return errors.New("this command takes no arguments")
	}
	if f := cmd.Root().String("format"); f != "" && f != "auto" {
		return errors.New("image preview commands use readable output; remove --format")
	}
	if cmd.Root().String("transform") != "" || cmd.Root().Bool("raw-output") {
		return errors.New("image preview commands cannot use --transform or --raw-output")
	}
	return nil
}

func setupImageInline(ctx context.Context, cmd *cli.Command) error {
	if err := checkImageInlineOutput(cmd); err != nil {
		return err
	}
	return terminalimage.Setup(ctx, cmd.Root().Writer)
}
func testImageInline(ctx context.Context, cmd *cli.Command) error {
	if err := checkImageInlineOutput(cmd); err != nil {
		return err
	}
	return terminalimage.Test(ctx, cmd.Root().Writer)
}
func statusImageInline(ctx context.Context, cmd *cli.Command) error {
	if err := checkImageInlineOutput(cmd); err != nil {
		return err
	}
	enabled, err := imageInlinePreference()
	if err != nil {
		return err
	}
	mode := "off"
	if enabled {
		mode = "on"
	}
	if _, err := fmt.Fprintf(cmd.Root().Writer, "Automatic previews: %s\n", mode); err != nil {
		return err
	}
	return terminalimage.Status(ctx, cmd.Root().Writer, cmd.Bool("check"), cmd.Bool("details"))
}
func resetImageInline(ctx context.Context, cmd *cli.Command) error {
	if err := checkImageInlineOutput(cmd); err != nil {
		return err
	}
	return terminalimage.Reset(ctx, cmd.Root().Writer)
}
