// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"context"
	"fmt"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

var uploadsPartsCreate = cli.Command{
	Name:    "create",
	Usage:   "Adds a\n[Part](https://platform.openai.com/docs/api-reference/uploads/part-object) to an\n[Upload](https://platform.openai.com/docs/api-reference/uploads/object) object.\nA Part represents a chunk of bytes from the file you are trying to upload.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "upload-id",
			Required:  true,
			PathParam: "upload_id",
		},
		&requestflag.Flag[string]{
			Name:      "data",
			Usage:     "The chunk of bytes for this Part.\n",
			Required:  true,
			BodyPath:  "data",
			FileInput: true,
		},
	},
	Action:          handleUploadsPartsCreate,
	HideHelpCommand: true,
}

func handleUploadsPartsCreate(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("upload-id") && len(unusedArgs) > 0 {
		cmd.Set("upload-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if len(unusedArgs) > 0 {
		return fmt.Errorf("Unexpected extra arguments: %v", unusedArgs)
	}

	options, err := flagOptions(
		cmd,
		apiquery.NestedQueryFormatBrackets,
		apiquery.ArrayQueryFormatBrackets,
		MultipartFormEncoded,
		false,
	)
	if err != nil {
		return err
	}

	params := openai.UploadPartNewParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Uploads.Parts.New(
		ctx,
		cmd.Value("upload-id").(string),
		params,
		options...,
	)
	if err != nil {
		return err
	}

	obj := gjson.ParseBytes(res)
	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	return ShowJSON(obj, ShowJSONOpts{
		ExplicitFormat: explicitFormat,
		Format:         format,
		RawOutput:      cmd.Root().Bool("raw-output"),
		Title:          "uploads:parts create",
		Transform:      transform,
	})
}
