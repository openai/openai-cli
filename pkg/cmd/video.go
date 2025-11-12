// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

var videosCreate = cli.Command{
	Name:    "create",
	Usage:   "Create a new video generation job from a prompt and optional reference assets.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:     "prompt",
			Usage:    "Text prompt that describes the video to generate.",
			Required: true,
			BodyPath: "prompt",
		},
		&requestflag.Flag[any]{
			Name:     "input-reference",
			Usage:    "Optional reference asset upload or reference object that guides generation.",
			BodyPath: "input_reference",
		},
		&requestflag.Flag[string]{
			Name:     "model",
			BodyPath: "model",
		},
		&requestflag.Flag[string]{
			Name:     "seconds",
			Usage:    `Allowed values: "4", "8", "12".`,
			BodyPath: "seconds",
		},
		&requestflag.Flag[string]{
			Name:     "size",
			Usage:    `Allowed values: "720x1280", "1280x720", "1024x1792", "1792x1024".`,
			BodyPath: "size",
		},
	},
	Action:          handleVideosCreate,
	HideHelpCommand: true,
}

var videosRetrieve = cli.Command{
	Name:    "retrieve",
	Usage:   "Fetch the latest metadata for a generated video.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "video-id",
			Required:  true,
			PathParam: "video_id",
		},
	},
	Action:          handleVideosRetrieve,
	HideHelpCommand: true,
}

var videosList = cli.Command{
	Name:    "list",
	Usage:   "List recently generated videos for the current project.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "after",
			Usage:     "Identifier for the last item from the previous pagination request",
			QueryPath: "after",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "Number of items to retrieve",
			QueryPath: "limit",
		},
		&requestflag.Flag[string]{
			Name:      "order",
			Usage:     "Sort order of results by timestamp. Use `asc` for ascending order or `desc` for descending order.",
			QueryPath: "order",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleVideosList,
	HideHelpCommand: true,
}

var videosDelete = cli.Command{
	Name:    "delete",
	Usage:   "Permanently delete a completed or failed video and its stored assets.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "video-id",
			Required:  true,
			PathParam: "video_id",
		},
	},
	Action:          handleVideosDelete,
	HideHelpCommand: true,
}

var videosCreateCharacter = cli.Command{
	Name:    "create-character",
	Usage:   "Create a character from an uploaded video.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:     "name",
			Usage:    "Display name for this API character.",
			Required: true,
			BodyPath: "name",
		},
		&requestflag.Flag[string]{
			Name:      "video",
			Usage:     "Video file used to create a character.",
			Required:  true,
			BodyPath:  "video",
			FileInput: true,
		},
	},
	Action:          handleVideosCreateCharacter,
	HideHelpCommand: true,
}

var videosDownloadContent = cli.Command{
	Name:    "download-content",
	Usage:   "Download the generated video bytes or a derived preview asset.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "video-id",
			Required:  true,
			PathParam: "video_id",
		},
		&requestflag.Flag[string]{
			Name:      "variant",
			Usage:     "Which downloadable asset to return. Defaults to the MP4 video.",
			QueryPath: "variant",
		},
		&requestflag.Flag[string]{
			Name:    "output",
			Aliases: []string{"o"},
			Usage:   "The file where the response contents will be stored. Use the value '-' to force output to stdout.",
		},
	},
	Action:          handleVideosDownloadContent,
	HideHelpCommand: true,
}

var videosEdit = cli.Command{
	Name:    "edit",
	Usage:   "Create a new video generation job by editing a source video or existing\ngenerated video.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:     "prompt",
			Usage:    "Text prompt that describes how to edit the source video.",
			Required: true,
			BodyPath: "prompt",
		},
		&requestflag.Flag[any]{
			Name:     "video",
			Usage:    "Reference to the completed video to edit.",
			Required: true,
			BodyPath: "video",
		},
	},
	Action:          handleVideosEdit,
	HideHelpCommand: true,
}

var videosExtend = cli.Command{
	Name:    "extend",
	Usage:   "Create an extension of a completed video.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:     "prompt",
			Usage:    "Updated text prompt that directs the extension generation.",
			Required: true,
			BodyPath: "prompt",
		},
		&requestflag.Flag[string]{
			Name:     "seconds",
			Usage:    `Allowed values: "4", "8", "12".`,
			Required: true,
			BodyPath: "seconds",
		},
		&requestflag.Flag[any]{
			Name:     "video",
			Usage:    "Reference to the completed video to extend.",
			Required: true,
			BodyPath: "video",
		},
	},
	Action:          handleVideosExtend,
	HideHelpCommand: true,
}

var videosGetCharacter = cli.Command{
	Name:    "get-character",
	Usage:   "Fetch a character.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "character-id",
			Required:  true,
			PathParam: "character_id",
		},
	},
	Action:          handleVideosGetCharacter,
	HideHelpCommand: true,
}

var videosRemix = cli.Command{
	Name:    "remix",
	Usage:   "Create a remix of a completed video using a refreshed prompt.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "video-id",
			Required:  true,
			PathParam: "video_id",
		},
		&requestflag.Flag[string]{
			Name:     "prompt",
			Usage:    "Updated text prompt that directs the remix generation.",
			Required: true,
			BodyPath: "prompt",
		},
	},
	Action:          handleVideosRemix,
	HideHelpCommand: true,
}

func handleVideosCreate(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()

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

	params := openai.VideoNewParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Videos.New(ctx, params, options...)
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
		Title:          "videos create",
		Transform:      transform,
	})
}

func handleVideosRetrieve(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("video-id") && len(unusedArgs) > 0 {
		cmd.Set("video-id", unusedArgs[0])
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
	_, err = client.Videos.Get(ctx, cmd.Value("video-id").(string), options...)
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
		Title:          "videos retrieve",
		Transform:      transform,
	})
}

func handleVideosList(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()

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

	params := openai.VideoListParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if format == "raw" {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.Videos.List(ctx, params, options...)
		if err != nil {
			return err
		}
		obj := gjson.ParseBytes(res)
		return ShowJSON(obj, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "videos list",
			Transform:      transform,
		})
	} else {
		iter := client.Videos.ListAutoPaging(ctx, params, options...)
		maxItems := int64(-1)
		if cmd.IsSet("max-items") {
			maxItems = cmd.Value("max-items").(int64)
		}
		return ShowJSONIterator(iter, maxItems, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "videos list",
			Transform:      transform,
		})
	}
}

func handleVideosDelete(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("video-id") && len(unusedArgs) > 0 {
		cmd.Set("video-id", unusedArgs[0])
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
	_, err = client.Videos.Delete(ctx, cmd.Value("video-id").(string), options...)
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
		Title:          "videos delete",
		Transform:      transform,
	})
}

func handleVideosCreateCharacter(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()

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

	params := openai.VideoNewCharacterParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Videos.NewCharacter(ctx, params, options...)
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
		Title:          "videos create-character",
		Transform:      transform,
	})
}

func handleVideosDownloadContent(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("video-id") && len(unusedArgs) > 0 {
		cmd.Set("video-id", unusedArgs[0])
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

	params := openai.VideoDownloadContentParams{}

	response, err := client.Videos.DownloadContent(
		ctx,
		cmd.Value("video-id").(string),
		params,
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

func handleVideosEdit(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()

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

	params := openai.VideoEditParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Videos.Edit(ctx, params, options...)
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
		Title:          "videos edit",
		Transform:      transform,
	})
}

func handleVideosExtend(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()

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

	params := openai.VideoExtendParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Videos.Extend(ctx, params, options...)
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
		Title:          "videos extend",
		Transform:      transform,
	})
}

func handleVideosGetCharacter(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("character-id") && len(unusedArgs) > 0 {
		cmd.Set("character-id", unusedArgs[0])
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
	_, err = client.Videos.GetCharacter(ctx, cmd.Value("character-id").(string), options...)
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
		Title:          "videos get-character",
		Transform:      transform,
	})
}

func handleVideosRemix(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("video-id") && len(unusedArgs) > 0 {
		cmd.Set("video-id", unusedArgs[0])
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

	params := openai.VideoRemixParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Videos.Remix(
		ctx,
		cmd.Value("video-id").(string),
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
		Title:          "videos remix",
		Transform:      transform,
	})
}
