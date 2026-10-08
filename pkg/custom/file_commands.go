package custom

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/urfave/cli/v3"
)

type fileCommandKey struct{}

const (
	fileUploadCommand = "upload"
	fileGetCommand    = "get"
)

// Extend task routes after cloning so compatibility commands keep their output.
func configureFileCommands(root *cli.Command) {
	files := root.Command("files")
	if files == nil {
		return
	}
	files.Usage = "Upload, inspect, and download files."
	files.Description = "Use upload for a local file, get for metadata, and download for contents.\nExisting create, retrieve, and content commands remain available."
	if upload := files.Command("upload"); upload != nil {
		upload.Usage = "Upload a local file with an explicit purpose."
		upload.UsageText = clihelp.Invocation(root.Name, os.Args) + " files upload [PATH | --file PATH] --purpose PURPOSE [options]"
		upload.Description = "Pass a plain local path. Paths with spaces need quotes.\nA leading @ is part of the filename; do not add @ to a plain path.\nUse --purpose PURPOSE -- -name for a filename starting with a dash.\nThe API validates file types and purposes. Upload success does not mean processing is complete."
		upload.Arguments = []cli.Argument{&cli.StringArgs{Name: "path", Max: 1}}
		next := upload.Action
		upload.Action = func(ctx context.Context, command *cli.Command) error {
			if command.Args().Present() {
				return fmt.Errorf("Unexpected extra arguments: %v", command.Args().Slice())
			}
			if paths := command.StringArgs("path"); len(paths) != 0 {
				if command.IsSet("file") {
					return errors.New("Use either a positional upload path or --file, not both.")
				}
				if paths[0] == "" {
					return errors.New("The upload path must not be empty.")
				}
				if err := command.Set("file", paths[0]); err != nil {
					return err
				}
			}
			return next(context.WithValue(ctx, fileCommandKey{}, fileUploadCommand), command)
		}
	}
	for _, route := range []struct{ name, source, usage string }{
		{"get", "retrieve", "Show file metadata, including its ID and creation time."},
		{"download", "content", "Download file contents to a path or stdout."},
	} {
		if files.Command(route.name) != nil {
			continue
		}
		source := files.Command(route.source)
		if source == nil {
			continue
		}
		command := cloneResourceCommand(source)
		command.Name, command.Aliases, command.Hidden = route.name, nil, false
		command.Usage = route.usage
		if route.name == fileGetCommand {
			command.Description = "Show metadata for a file. Use files download to retrieve its contents."
			next := command.Action
			command.Action = func(ctx context.Context, command *cli.Command) error {
				return next(context.WithValue(ctx, fileCommandKey{}, fileGetCommand), command)
			}
		} else {
			command.Description = "Use --output PATH to save the contents, or redirect stdout to a file.\nUse --output - to force stdout. Existing download behavior also applies in a terminal."
		}
		files.Commands = append(files.Commands, command)
		markCompatibilityCommand(source)
	}
	for name, rank := range map[string]int{"upload": 10, "get": 20, "download": 30} {
		if command := files.Command(name); command != nil {
			if command.Metadata == nil {
				command.Metadata = map[string]any{}
			}
			command.Metadata["help-command-rank"] = rank
		}
	}
}
