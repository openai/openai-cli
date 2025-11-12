// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/urfave/cli/v3"
)

var containersFilesContentRetrieve = cli.Command{
	Name:    "retrieve",
	Usage:   "Retrieve Container File Content",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "container-id",
			Required:  true,
			PathParam: "container_id",
		},
		&requestflag.Flag[string]{
			Name:      "file-id",
			Required:  true,
			PathParam: "file_id",
		},
		&requestflag.Flag[string]{
			Name:    "output",
			Aliases: []string{"o"},
			Usage:   "The file where the response contents will be stored. Use the value '-' to force output to stdout.",
		},
	},
	Action:          handleContainersFilesContentRetrieve,
	HideHelpCommand: true,
}

func handleContainersFilesContentRetrieve(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("container-id") && len(unusedArgs) > 0 {
		cmd.Set("container-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if !cmd.IsSet("file-id") && len(unusedArgs) > 0 {
		cmd.Set("file-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if len(unusedArgs) > 0 {
		return fmt.Errorf("Unexpected extra arguments: %v", unusedArgs)
	}

	options, err := flagOptions(
		cmd,
		apiquery.NestedQueryFormatBrackets,
		apiquery.ArrayQueryFormatBrackets,
		EmptyBody,
		false,
	)
	if err != nil {
		return err
	}

	response, err := client.Containers.Files.Content.Get(
		ctx,
		cmd.Value("container-id").(string),
		cmd.Value("file-id").(string),
		options...,
	)
	if err != nil {
		return err
	}
	message, err := writeBinaryResponse(response, os.Stdout, cmd.String("output"))
	if message != "" {
		fmt.Println(message)
	}
	return err
}
