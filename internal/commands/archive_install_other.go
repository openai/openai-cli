//go:build !windows

package commands

import "github.com/urfave/cli/v3"

func RegisterArchiveInstall(*cli.Command) {}
