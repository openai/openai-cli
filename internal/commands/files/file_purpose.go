package files

import (
	"context"
	"io"
	"os"

	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-cli/internal/input"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

const purposeContextMetadata = "openai-file-purpose-context"

// RequestInput borrows stdin without closing it or changing its descriptor state.
// Scalar inspection uses the shared platform-specific cancellation behavior.
func RequestInput(command *cli.Command, reader io.Reader) io.Reader {
	ctx, _ := command.Metadata[purposeContextMetadata].(context.Context)
	file, ok := reader.(*os.File)
	if ctx == nil || !ok {
		return reader
	}
	return purposeInput{context: ctx, file: file}
}

type purposeInput struct {
	context context.Context
	file    *os.File
}

func (reader purposeInput) Read(data []byte) (int, error) {
	if err := reader.context.Err(); err != nil {
		return 0, err
	}
	return input.ReadFile(reader.context, reader.file, data)
}

func configurePurposeValidation(root *cli.Command) {
	for _, resource := range []string{"files", "uploads"} {
		group := root.Command(resource)
		if group == nil {
			continue
		}
		for _, command := range group.Commands {
			if command.Action == nil || (command.Name != "create" && command.Name != "upload" && command.Name != "list") {
				continue
			}
			next := command.Action
			command.Action = func(ctx context.Context, command *cli.Command) error {
				if command.Metadata == nil {
					command.Metadata = map[string]any{}
				}
				command.Metadata[purposeContextMetadata] = ctx
				defer delete(command.Metadata, purposeContextMetadata)
				return next(ctx, command)
			}
		}
	}
}

// ValidatePurpose checks resolved fields, including scalar files, before sending a request.
func ValidatePurpose(command *cli.Command, contents requestflag.RequestContents) error {
	resource := localcommands.ResourceName(command)
	var fields map[string]any
	switch {
	case resource == "files" && command.Name == "list":
		fields = contents.Queries
	case resource == "files" && (command.Name == "create" || command.Name == "upload"),
		resource == "uploads" && command.Name == "create":
		fields, _ = contents.Body.(map[string]any)
	default:
		return nil
	}
	value, exists := fields["purpose"]
	if !exists {
		return nil
	}
	ctx, _ := command.Metadata[purposeContextMetadata].(context.Context)
	if ctx == nil {
		ctx = context.Background()
	}
	inspected, replay, err := requestconfig.ReadMultipartScalar(ctx, "purpose", value)
	fields["purpose"] = replay
	if err != nil {
		return err
	}
	if purpose, ok := inspected.(string); ok {
		switch purpose {
		case "fine-tune", "fine-tune-results":
			return &localcommands.LocalError{Message: "This file purpose is unavailable in the CLI. Use the Platform API directly."}
		}
	}
	return nil
}
