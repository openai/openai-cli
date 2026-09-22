package custom

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openai/openai-cli/internal/imageprefs"
	"github.com/urfave/cli/v3"
)

func imageInlinePreferencePath() (string, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "openai", "image-preferences.json"), nil
}

func imageInlinePreference() (bool, error) {
	path, err := imageInlinePreferencePath()
	if err != nil {
		return false, err
	}
	return imageprefs.Load(path)
}

func imageInlinePreferenceCommands() []*cli.Command {
	var commands []*cli.Command
	for _, mode := range []string{"on", "off"} {
		commands = append(commands, &cli.Command{
			Name: mode, Usage: "Remember inline previews " + mode + " for image generation.",
			Description: "Applies to future image generations on this computer. No API call or key is needed.\n--inline on or --inline off overrides this preference for one generation.\nThe images preview command still displays a file when explicitly requested.",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if cmd.Args().Len() != 0 {
					return errors.New("this command takes no arguments")
				}
				if format := strings.ToLower(cmd.Root().String("format")); format != "" && format != "auto" {
					return errors.New("image preferences use readable output; remove --format")
				}
				if cmd.Root().String("transform") != "" || cmd.Root().Bool("raw-output") {
					return errors.New("image preferences cannot use --transform or --raw-output")
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				path, err := imageInlinePreferencePath()
				if err != nil {
					return err
				}
				if err := imageprefs.Save(path, mode == "on"); err != nil {
					return fmt.Errorf("save inline preference: %w", err)
				}
				_, err = fmt.Fprintf(cmd.Root().Writer, "Inline previews %s for future image generations.\nOverride once with --inline on or --inline off.\n", mode)
				return err
			},
		})
	}
	return commands
}
