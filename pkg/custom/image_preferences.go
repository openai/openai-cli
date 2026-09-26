package custom

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/openai/openai-cli/internal/imageprefs"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/urfave/cli/v3"
)

func imageInlinePreferencePath() (string, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "openai", "image-preferences.json"), nil
}

// An explicit flag, including auto, bypasses preferences. Pipes never need to
// read this terminal-only preference and cannot activate a font.
func imageInlineMode(ctx context.Context, command *cli.Command, terminal bool) (string, error) {
	mode := command.String("inline")
	if mode != "auto" && mode != "on" && mode != "off" {
		return "", imageSavingFailure("--inline must be auto, on or off.", nil)
	}
	if command.IsSet("inline") || !terminal {
		return mode, nil
	}
	path, err := imageInlinePreferencePath()
	if err != nil {
		return "off", err
	}
	return imageprefs.Load(ctx, path)
}

func imageInlinePreferenceCommands() []*cli.Command {
	var commands []*cli.Command
	for _, mode := range []string{"on", "off"} {
		commands = append(commands, &cli.Command{
			Name: mode, Usage: "Remember automatic image previews " + mode + ".",
			Description: "Applies to future image generation, edits and variations on this computer. No API request or key is needed.\nPer-command --inline auto, on or off overrides it. Explicit images preview ignores this preference.\nTurning on also allows image-font activation in a local Apple Terminal tab.",
			Action: func(ctx context.Context, command *cli.Command) error {
				if command.Args().Len() != 0 {
					return imageSavingFailure("This command takes no arguments. Use openai images inline on or openai images inline off.", nil)
				}
				if err := validateLocalImageOutput(command); err != nil {
					return err
				}
				path, err := imageInlinePreferencePath()
				if err != nil {
					return imageSavingFailure("Could not find your configuration folder. Check HOME, APPDATA or XDG_CONFIG_HOME.", err)
				}
				if err := imageprefs.Save(ctx, path, mode == "on"); err != nil {
					return imageSavingFailure(fmt.Sprintf("Could not save the inline preference. Existing settings were kept. Check permissions and the settings in %q; move an invalid file aside before trying again.", path), err)
				}
				message := "Automatic image previews " + mode + ".\nOverride once: --inline auto|on|off\nView a saved image: openai images preview FILE"
				if mode == "on" {
					message += "\nOn also permits image-font activation in local Apple Terminal."
				}
				return readable.WriteText(imageCommandWriter(command), message)
			},
		})
	}
	return commands
}

func validateLocalImageOutput(command *cli.Command) error {
	root := command.Root()
	if format := strings.ToLower(root.String("format")); format != "" && format != "auto" && format != "text" {
		return imageSavingFailure("This local image command uses readable output; use --format auto or text.", nil)
	}
	if root.String("transform") != "" || root.Bool("raw-output") {
		return imageSavingFailure("This local image command cannot use --transform or --raw-output.", nil)
	}
	return nil
}

func imageCommandWriter(command *cli.Command) io.Writer {
	if out := command.Root().Writer; out != nil {
		return out
	}
	return os.Stdout
}
