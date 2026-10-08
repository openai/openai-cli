package custom

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/urfave/cli/v3"
)

type fileCommandKey struct{}
type fileInvocationKey struct{}

type fileInvocation struct {
	display    string
	executable string
	goRun      bool
}

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
	if files.Metadata == nil {
		files.Metadata = map[string]any{}
	}
	files.Metadata["help-content"] = clihelp.Content{
		Description: files.Description,
		Examples: []clihelp.Example{
			{Description: "Upload a local file:", Command: `files upload "upload space.txt" --purpose user_data`},
			{Description: "Inspect its metadata:", Command: `files get file-example`},
			{Description: "Save its contents:", Command: `files download file-example --output "downloaded copy.txt"`},
		},
	}
	if upload := files.Command("upload"); upload != nil {
		if upload.Metadata == nil {
			upload.Metadata = map[string]any{}
		}
		upload.Metadata["completion-positional-file"] = "file"
		upload.Usage = "Upload a local file with an explicit purpose."
		upload.UsageText = clihelp.Invocation(root.Name, os.Args) + " files upload [PATH | --file PATH] --purpose PURPOSE [options]"
		upload.Description = "Pass a plain local path. Paths with spaces need quotes.\nA leading @ is part of the filename; do not add @ to a plain path.\nUse --purpose PURPOSE -- -name for a filename starting with a dash.\nThe API validates file types and purposes. Upload success does not mean processing is complete."
		upload.Metadata["help-content"] = clihelp.Content{
			InputNote: "A path is required: use PATH or --file PATH. Choose --purpose explicitly. " +
				"The existing piped JSON/YAML keys are file and purpose.",
			Description: upload.Description,
			Examples: []clihelp.Example{
				{Description: "Upload an existing file:", Command: `files upload "upload space.txt" --purpose user_data`},
			},
		}
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
			invocation := fileInvocation{display: errorHelpInvocation(command.Root())}
			invocation.goRun = invocation.display == "go run ./cmd/"+command.Root().Name
			if len(os.Args) > 0 {
				invocation.executable = os.Args[0]
				if strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") != command.Root().Name ||
					strings.IndexFunc(os.Args[0], unicode.IsControl) >= 0 {
					invocation.display = os.Args[0]
					invocation.goRun = false
				}
			}
			ctx = context.WithValue(ctx, fileInvocationKey{}, invocation)
			return next(context.WithValue(ctx, fileCommandKey{}, fileUploadCommand), command)
		}
	}
	for _, route := range []struct{ name, source, usage string }{
		{"get", "retrieve", "Inspect file metadata."},
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
