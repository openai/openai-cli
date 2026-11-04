//go:build windows

package commands

import (
	"context"
	"fmt"

	"github.com/openai/openai-cli/internal/updatenotice"
	"github.com/urfave/cli/v3"
)

// RegisterArchiveInstall keeps the portable release usable until its owner
// explicitly requests a per-user installation.
func RegisterArchiveInstall(root *cli.Command) {
	root.Commands = append(root.Commands, &cli.Command{
		Name:        "install",
		Usage:       "Install this Windows ZIP for your user and enable automatic updates",
		Description: "Print the managed executable path. Setup reuses an existing managed installation.",
		Metadata:    map[string]any{LocalUtilityMetadata: true, "help-command-section": "Local tools"},
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "user", Usage: "Set up the current user in an ordinary terminal", Required: true, OnlyOnce: true},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if !cmd.Bool("user") || cmd.Args().Len() != 0 {
				return &LocalError{Message: "Use openai install --user without other arguments."}
			}
			path, err := updatenotice.InstallWindowsArchive(ctx)
			if err != nil {
				return &LocalError{Message: err.Error(), Cause: err}
			}
			_, err = fmt.Fprintln(cmd.Root().Writer, path)
			return err
		},
	})
}
