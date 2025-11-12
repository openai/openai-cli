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

var uploadsCreate = requestflag.WithInnerFlags(cli.Command{
	Name:    "create",
	Usage:   "Creates an intermediate\n[Upload](https://platform.openai.com/docs/api-reference/uploads/object) object\nthat you can add\n[Parts](https://platform.openai.com/docs/api-reference/uploads/part-object) to.\nCurrently, an Upload can accept at most 8 GB in total and expires after an hour\nafter you create it.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[int64]{
			Name:     "bytes",
			Usage:    "The number of bytes in the file you are uploading.\n",
			Required: true,
			BodyPath: "bytes",
		},
		&requestflag.Flag[string]{
			Name:     "filename",
			Usage:    "The name of the file to upload.\n",
			Required: true,
			BodyPath: "filename",
		},
		&requestflag.Flag[string]{
			Name:     "mime-type",
			Usage:    "The MIME type of the file.\n\n\nThis must fall within the supported MIME types for your file purpose. See\nthe supported MIME types for assistants and vision.\n",
			Required: true,
			BodyPath: "mime_type",
		},
		&requestflag.Flag[string]{
			Name:     "purpose",
			Usage:    "The intended purpose of the uploaded file. One of:\n- `assistants`: Used in the Assistants API\n- `batch`: Used in the Batch API\n- `fine-tune`: Used for fine-tuning\n- `vision`: Images used for vision fine-tuning\n- `user_data`: Flexible file type for any purpose\n- `evals`: Used for eval data sets\n",
			Required: true,
			BodyPath: "purpose",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "expires-after",
			Usage:    "The expiration policy for a file. By default, files with `purpose=batch` expire after 30 days and all other files are persisted until they are manually deleted.",
			BodyPath: "expires_after",
		},
	},
	Action:          handleUploadsCreate,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"expires-after": {
		&requestflag.InnerFlag[string]{
			Name:       "expires-after.anchor",
			Usage:      "Anchor timestamp after which the expiration policy applies. Supported anchors: `created_at`.",
			InnerField: "anchor",
		},
		&requestflag.InnerFlag[int64]{
			Name:       "expires-after.seconds",
			Usage:      "The number of seconds after the anchor time that the file will expire. Must be between 3600 (1 hour) and 2592000 (30 days).",
			InnerField: "seconds",
		},
	},
})

var uploadsCancel = cli.Command{
	Name:    "cancel",
	Usage:   "Cancels the Upload. No Parts may be added after an Upload is cancelled.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "upload-id",
			Required:  true,
			PathParam: "upload_id",
		},
	},
	Action:          handleUploadsCancel,
	HideHelpCommand: true,
}

var uploadsComplete = cli.Command{
	Name:    "complete",
	Usage:   "Completes the\n[Upload](https://platform.openai.com/docs/api-reference/uploads/object).",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "upload-id",
			Required:  true,
			PathParam: "upload_id",
		},
		&requestflag.Flag[[]string]{
			Name:     "part-id",
			Usage:    "The ordered list of Part IDs.\n",
			Required: true,
			BodyPath: "part_ids",
		},
		&requestflag.Flag[string]{
			Name:     "md5",
			Usage:    "The optional md5 checksum for the file contents to verify if the bytes uploaded matches what you expect.\n",
			BodyPath: "md5",
		},
	},
	Action:          handleUploadsComplete,
	HideHelpCommand: true,
}

func handleUploadsCreate(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()

	if len(unusedArgs) > 0 {
		return fmt.Errorf("Unexpected extra arguments: %v", unusedArgs)
	}

	options, err := flagOptions(
		cmd,
		apiquery.NestedQueryFormatBrackets,
		apiquery.ArrayQueryFormatBrackets,
		ApplicationJSON,
		false,
	)
	if err != nil {
		return err
	}

	params := openai.UploadNewParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Uploads.New(ctx, params, options...)
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
		Title:          "uploads create",
		Transform:      transform,
	})
}

func handleUploadsCancel(ctx context.Context, cmd *cli.Command) error {
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
		EmptyBody,
		false,
	)
	if err != nil {
		return err
	}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Uploads.Cancel(ctx, cmd.Value("upload-id").(string), options...)
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
		Title:          "uploads cancel",
		Transform:      transform,
	})
}

func handleUploadsComplete(ctx context.Context, cmd *cli.Command) error {
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
		ApplicationJSON,
		false,
	)
	if err != nil {
		return err
	}

	params := openai.UploadCompleteParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Uploads.Complete(
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
		Title:          "uploads complete",
		Transform:      transform,
	})
}
