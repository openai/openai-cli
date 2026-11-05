//go:build windows

package commands

import (
	"context"
	"fmt"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/updatenotice"
	"github.com/urfave/cli/v3"
)

// RegisterArchiveInstall keeps the portable release usable until its owner
// explicitly requests a per-user installation.
func RegisterArchiveInstall(root *cli.Command) {
	const description = "Print the managed executable path. Setup reuses an existing managed installation."
	root.Commands = append(root.Commands, &cli.Command{
		Name:        "install",
		Usage:       "Install this Windows ZIP for your user and enable automatic updates",
		Description: description,
		Metadata: map[string]any{LocalUtilityMetadata: true, "help-command-section": "Local tools", "help-content": clihelp.Content{
			Description: description, GlobalOptionsNote: "Use --format-error json for machine-readable installation errors.",
		}},
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "user", Usage: "Set up the current user in an ordinary terminal", Required: true, OnlyOnce: true},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if !cmd.Bool("user") || cmd.Args().Len() != 0 {
				return &LocalError{Message: "Use openai install --user without other arguments."}
			}
			path, err := updatenotice.InstallWindowsArchive(ctx)
			if err == updatenotice.ErrWindowsLauncherPathConflict {
				// Only Install.cmd requests this successful, attention-required status.
				return cli.Exit("", 86)
			}
			if err != nil {
				return &LocalError{Message: err.Error(), Cause: err}
			}
			_, err = fmt.Fprintln(cmd.Root().Writer, path)
			return err
		},
	})
}
