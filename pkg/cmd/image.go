// File generated from our OpenAPI spec by Castiron. See CONTRIBUTING.md for details.

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

var imagesCreateVariation = cli.Command{
	Name:    "create-variation",
	Usage:   "Legacy endpoint for creating variations with DALL·E 2, which was retired from\nthe API on May 12, 2026. See\n[deprecations](https://developers.openai.com/api/docs/deprecations). For new\nintegrations, use image edits with a supported GPT Image model; see the\n[image generation guide](https://developers.openai.com/api/docs/guides/image-generation).",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "image",
			Usage:     "The input image for the legacy variations endpoint. The legacy format requires a valid PNG file, less than 4MB, and square.",
			Required:  true,
			BodyPath:  "image",
			FileInput: true,
		},
		&requestflag.Flag[*string]{
			Name:     "model",
			Usage:    "Legacy model selection for the variations endpoint, which was designed for `dall-e-2`. DALL·E 2 was retired from the API on May 12, 2026; see [deprecations](https://developers.openai.com/api/docs/deprecations). Use image edits with a supported GPT Image model for new integrations.",
			BodyPath: "model",
		},
		&requestflag.Flag[*int64]{
			Name:     "n",
			Usage:    "The number of images requested from the legacy variations endpoint. Must be between 1 and 10.",
			Default:  requestflag.Ptr[int64](1),
			BodyPath: "n",
		},
		&requestflag.Flag[*string]{
			Name:     "response-format",
			Usage:    "The response format for the legacy variations endpoint: `url` or `b64_json`. Returned URLs were valid for 60 minutes after image generation.",
			Default:  requestflag.Ptr[string]("url"),
			BodyPath: "response_format",
		},
		&requestflag.Flag[*string]{
			Name:     "size",
			Usage:    "The requested image size for the legacy variations endpoint. Must be one of `256x256`, `512x512`, or `1024x1024`.",
			Default:  requestflag.Ptr[string]("1024x1024"),
			BodyPath: "size",
		},
		&requestflag.Flag[string]{
			Name:     "user",
			Usage:    "A unique identifier representing your end-user, which can help OpenAI to monitor and detect abuse. [Learn more](https://developers.openai.com/api/docs/guides/safety-best-practices#implement-safety-identifiers).\n",
			BodyPath: "user",
		},
	},
	Action:          handleImagesCreateVariation,
	HideHelpCommand: true,
}

var imagesEdit = cli.Command{
	Name:    "edit",
	Usage:   "Creates an edited or extended image given one or more source images and a\nprompt. This endpoint supports GPT Image models. DALL·E 2 was retired from the\nAPI on May 12, 2026; see\n[deprecations](https://developers.openai.com/api/docs/deprecations).",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[[]string]{
			Name:      "image",
			Usage:     "The image(s) to edit. Must be a supported image file or an array of images.\n\nFor the GPT image models (`gpt-image-1`, `gpt-image-1-mini`, `gpt-image-1.5`,\n`gpt-image-2`, `gpt-image-2-2026-04-21`, `gpt-image-2.5-sunburst`,\n`gpt-image-2.5-sunburst-2026-09-08`, `gpt-image-2.5-flare`,\n`gpt-image-2.5-flare-2026-09-08`, and `chatgpt-image-latest`), each image\nshould be a `png`, `webp`, or `jpg` file less than 50MB. You can provide up to\n16 images.\n",
			Required:  true,
			BodyPath:  "image",
			FileInput: true,
		},
		&requestflag.Flag[string]{
			Name:     "prompt",
			Usage:    "A text description of the desired image(s). The maximum length is 32000 characters for the GPT image models.",
			Required: true,
			BodyPath: "prompt",
		},
		&requestflag.Flag[*string]{
			Name:     "background",
			Usage:    "Allows to set transparency for the background of the generated image(s). Must\nbe one of `transparent`, `opaque`, or `auto` (default value). When `auto` is\nused, the model will automatically determine the best background for the\nimage.\n\n`gpt-image-2.5-sunburst` and `gpt-image-2.5-flare`, including their\n`2026-09-08` snapshots, support `opaque` and `transparent` backgrounds.\nTransparent backgrounds are available for supported GPT Image models. For\n`gpt-image-2` and `gpt-image-2-2026-04-21`, this support is in preview. When\nusing `transparent`, set the output format to `png` or `webp`.\n",
			Default:  requestflag.Ptr[string]("auto"),
			BodyPath: "background",
		},
		&requestflag.Flag[*string]{
			Name:     "input-fidelity",
			Usage:    "Control how much effort the model will exert to match the style and features, especially facial features, of input images. Models that accept both `high` and `low` include `gpt-image-1`, `gpt-image-1.5`, and `chatgpt-image-latest`. `gpt-image-1-mini` accepts only `low`. Defaults to `low` on models that support this parameter. Omit this parameter for `gpt-image-2`, `gpt-image-2-2026-04-21`, and other models that do not support it. See the [image input fidelity guide](https://developers.openai.com/api/docs/guides/image-generation#image-input-fidelity) for model-specific guidance.",
			BodyPath: "input_fidelity",
		},
		&requestflag.Flag[string]{
			Name:      "mask",
			Usage:     "An additional image whose fully transparent areas (e.g. where alpha is zero) indicate where `image` should be edited. If there are multiple images provided, the mask will be applied on the first image. Must be a valid PNG file, less than 4MB, and have the same dimensions as `image`.",
			BodyPath:  "mask",
			FileInput: true,
		},
		&requestflag.Flag[*string]{
			Name:     "model",
			Usage:    "The GPT Image model to use for image editing (`gpt-image-1`, `gpt-image-1-mini`, `gpt-image-1.5`, `gpt-image-2`, `gpt-image-2-2026-04-21`, `gpt-image-2.5-sunburst`, `gpt-image-2.5-sunburst-2026-09-08`, `gpt-image-2.5-flare`, `gpt-image-2.5-flare-2026-09-08`, or `chatgpt-image-latest`). Defaults to `gpt-image-1.5`. DALL·E 2 was retired from the API on May 12, 2026; see [deprecations](https://developers.openai.com/api/docs/deprecations).",
			BodyPath: "model",
		},
		&requestflag.Flag[*int64]{
			Name:     "n",
			Usage:    "The number of images to generate. Must be between 1 and 10.",
			Default:  requestflag.Ptr[int64](1),
			BodyPath: "n",
		},
		&requestflag.Flag[*int64]{
			Name:     "output-compression",
			Usage:    "The compression level (0-100%) for the generated images. This parameter\nis only supported for the GPT image models with the `webp` or `jpeg` output\nformats, and defaults to 100.\n",
			Default:  requestflag.Ptr[int64](100),
			BodyPath: "output_compression",
		},
		&requestflag.Flag[*string]{
			Name:     "output-format",
			Usage:    "The format in which the generated images are returned. This parameter is\nonly supported for the GPT image models. Must be one of `png`, `jpeg`, or `webp`.\nThe default value is `png`.\n",
			Default:  requestflag.Ptr[string]("png"),
			BodyPath: "output_format",
		},
		&requestflag.Flag[*int64]{
			Name:     "partial-images",
			Usage:    "The number of partial images to generate. This parameter is used for\nstreaming responses that return partial images. Value must be between 0 and 3.\nWhen set to 0, the response will be a single image sent in one streaming event.\n\nNote that the final image may be sent before the full number of partial images\nare generated if the full image is generated more quickly.\n",
			Default:  requestflag.Ptr[int64](0),
			BodyPath: "partial_images",
		},
		&requestflag.Flag[*string]{
			Name:     "quality",
			Usage:    "The quality of the image that will be generated for GPT image models. The GPT image models support `low`, `medium`, and `high`. `gpt-image-2.5-sunburst` and `gpt-image-2.5-flare`, including their `2026-09-08` snapshots, also support `xhigh` and `max`. Defaults to `auto`.\n",
			Default:  requestflag.Ptr[string]("auto"),
			BodyPath: "quality",
		},
		&requestflag.Flag[*string]{
			Name:     "response-format",
			Usage:    "Legacy response-format parameter (`url` or `b64_json`) for the retired `dall-e-2` model. GPT Image models always return base64-encoded images.",
			BodyPath: "response_format",
		},
		&requestflag.Flag[*string]{
			Name:     "size",
			Usage:    "The size of the generated images. For `gpt-image-2`, `gpt-image-2-2026-04-21`, `gpt-image-2.5-sunburst`, `gpt-image-2.5-sunburst-2026-09-08`, `gpt-image-2.5-flare`, and `gpt-image-2.5-flare-2026-09-08`, arbitrary resolutions are supported as `WIDTHxHEIGHT` strings, for example `1536x864`. Width and height must both be divisible by 16 and the requested aspect ratio must be between 1:3 and 3:1. Resolutions above `2560x1440` are experimental, and the maximum supported resolution is `3840x2160`. The requested size must also satisfy the model's current pixel and edge limits. The standard sizes `1024x1024`, `1536x1024`, and `1024x1536` are supported by the GPT image models; `auto` is supported for models that allow automatic sizing.",
			BodyPath: "size",
		},
		&requestflag.Flag[*bool]{
			Name:     "stream",
			Usage:    "Edit the image in streaming mode. Defaults to `false`. See the\n[Image generation guide](https://developers.openai.com/api/docs/guides/image-generation) for more information.\n",
			Default:  requestflag.Ptr[bool](false),
			BodyPath: "stream",
		},
		&requestflag.Flag[string]{
			Name:     "user",
			Usage:    "A unique identifier representing your end-user, which can help OpenAI to monitor and detect abuse. [Learn more](https://developers.openai.com/api/docs/guides/safety-best-practices#implement-safety-identifiers).\n",
			BodyPath: "user",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleImagesEdit,
	HideHelpCommand: true,
}

var imagesGenerate = cli.Command{
	Name:    "generate",
	Usage:   "Creates an image given a prompt.\n[Learn more](https://developers.openai.com/api/docs/guides/images-vision).",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:     "prompt",
			Usage:    "A text description of the desired image(s). The maximum length is 32000 characters for the GPT image models. Legacy limits for the retired models were 1000 characters for `dall-e-2` and 4000 characters for `dall-e-3`.",
			Required: true,
			BodyPath: "prompt",
		},
		&requestflag.Flag[*string]{
			Name:     "background",
			Usage:    "Allows to set transparency for the background of the generated image(s). Must\nbe one of `transparent`, `opaque`, or `auto` (default value). When `auto` is\nused, the model will automatically determine the best background for the\nimage.\n\n`gpt-image-2.5-sunburst` and `gpt-image-2.5-flare`, including their\n`2026-09-08` snapshots, support `opaque` and `transparent` backgrounds.\nTransparent backgrounds are available for supported GPT Image models. For\n`gpt-image-2` and `gpt-image-2-2026-04-21`, this support is in preview. When\nusing `transparent`, set the output format to `png` or `webp`.\n",
			Default:  requestflag.Ptr[string]("auto"),
			BodyPath: "background",
		},
		&requestflag.Flag[*string]{
			Name:     "model",
			Usage:    "The GPT Image model to use for image generation. Specify a supported model explicitly, such as `gpt-image-1`, `gpt-image-1-mini`, `gpt-image-1.5`, `gpt-image-2`, `gpt-image-2-2026-04-21`, `gpt-image-2.5-sunburst`, `gpt-image-2.5-sunburst-2026-09-08`, `gpt-image-2.5-flare`, or `gpt-image-2.5-flare-2026-09-08`. DALL·E 2 (`dall-e-2`) and DALL·E 3 (`dall-e-3`) were retired from the API on May 12, 2026; see [deprecations](https://developers.openai.com/api/docs/deprecations).",
			BodyPath: "model",
		},
		&requestflag.Flag[*string]{
			Name:     "moderation",
			Usage:    "Control the content-moderation level for images generated by the GPT image models. Must be either `low` for less restrictive filtering or `auto` (default value).",
			Default:  requestflag.Ptr[string]("auto"),
			BodyPath: "moderation",
		},
		&requestflag.Flag[*int64]{
			Name:     "n",
			Usage:    "The number of images to generate. Must be between 1 and 10. The retired `dall-e-3` model only supported `n=1`.",
			Default:  requestflag.Ptr[int64](1),
			BodyPath: "n",
		},
		&requestflag.Flag[*int64]{
			Name:     "output-compression",
			Usage:    "The compression level (0-100%) for the generated images. This parameter is only supported for the GPT image models with the `webp` or `jpeg` output formats, and defaults to 100.",
			Default:  requestflag.Ptr[int64](100),
			BodyPath: "output_compression",
		},
		&requestflag.Flag[*string]{
			Name:     "output-format",
			Usage:    "The format in which the generated images are returned. This parameter is only supported for the GPT image models. Must be one of `png`, `jpeg`, or `webp`.",
			Default:  requestflag.Ptr[string]("png"),
			BodyPath: "output_format",
		},
		&requestflag.Flag[*int64]{
			Name:     "partial-images",
			Usage:    "The number of partial images to generate. This parameter is used for\nstreaming responses that return partial images. Value must be between 0 and 3.\nWhen set to 0, the response will be a single image sent in one streaming event.\n\nNote that the final image may be sent before the full number of partial images\nare generated if the full image is generated more quickly.\n",
			Default:  requestflag.Ptr[int64](0),
			BodyPath: "partial_images",
		},
		&requestflag.Flag[*string]{
			Name:     "quality",
			Usage:    "The quality of the image that will be generated.\n\n- `auto` (default value) will automatically select the best quality for the given\n  model.\n- `high`, `medium` and `low` are supported for the GPT image models.\n- `gpt-image-2.5-sunburst` and `gpt-image-2.5-flare`, including their `2026-09-08`\n  snapshots, also support `xhigh` and `max`.\n- The retired `dall-e-3` model supported the legacy values `hd` and `standard`.\n- The retired `dall-e-2` model only supported the legacy value `standard`.\n",
			Default:  requestflag.Ptr[string]("auto"),
			BodyPath: "quality",
		},
		&requestflag.Flag[*string]{
			Name:     "response-format",
			Usage:    "Legacy response format: `url` or `b64_json`, for the retired `dall-e-2` and `dall-e-3` models. Returned URLs were valid for 60 minutes after image generation. This parameter is not supported for the GPT image models, which always return base64-encoded images.",
			Default:  requestflag.Ptr[string]("url"),
			BodyPath: "response_format",
		},
		&requestflag.Flag[*string]{
			Name:     "size",
			Usage:    "The size of the generated images. For `gpt-image-2`, `gpt-image-2-2026-04-21`, `gpt-image-2.5-sunburst`, `gpt-image-2.5-sunburst-2026-09-08`, `gpt-image-2.5-flare`, and `gpt-image-2.5-flare-2026-09-08`, arbitrary resolutions are supported as `WIDTHxHEIGHT` strings, for example `1536x864`. Width and height must both be divisible by 16 and the requested aspect ratio must be between 1:3 and 3:1. Resolutions above `2560x1440` are experimental, and the maximum supported resolution is `3840x2160`. The requested size must also satisfy the model's current pixel and edge limits. The standard sizes `1024x1024`, `1536x1024`, and `1024x1536` are supported by the GPT image models; `auto` is supported for models that allow automatic sizing. Legacy sizes for the retired `dall-e-2` model were `256x256`, `512x512`, and `1024x1024`. Legacy sizes for the retired `dall-e-3` model were `1024x1024`, `1792x1024`, and `1024x1792`.",
			BodyPath: "size",
		},
		&requestflag.Flag[*bool]{
			Name:     "stream",
			Usage:    "Generate the image in streaming mode. Defaults to `false`. See the\n[Image generation guide](https://developers.openai.com/api/docs/guides/image-generation) for more information.\nThis parameter is only supported for the GPT image models.\n",
			Default:  requestflag.Ptr[bool](false),
			BodyPath: "stream",
		},
		&requestflag.Flag[*string]{
			Name:     "style",
			Usage:    "Legacy style options `vivid` and `natural` for the retired `dall-e-3` model. Vivid produced hyper-real and dramatic images; natural produced more natural, less hyper-real looking images. This parameter is not supported for the GPT image models.",
			Default:  requestflag.Ptr[string]("vivid"),
			BodyPath: "style",
		},
		&requestflag.Flag[string]{
			Name:     "user",
			Usage:    "A unique identifier representing your end-user, which can help OpenAI to monitor and detect abuse. [Learn more](https://developers.openai.com/api/docs/guides/safety-best-practices#implement-safety-identifiers).\n",
			BodyPath: "user",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleImagesGenerate,
	HideHelpCommand: true,
}

func handleImagesCreateVariation(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.ImageNewVariationParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Images.NewVariation(ctx, params, options...)
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
		Title:          "images create-variation",
		Transform:      transform,
	})
}

func handleImagesEdit(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.ImageEditParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if streamFlagValue, _ := cmd.Value("stream").(*bool); streamFlagValue != nil && *streamFlagValue {
		stream := client.Images.EditStreaming(ctx, params, options...)
		maxItems := int64(-1)
		if cmd.IsSet("max-items") {
			maxItems = cmd.Value("max-items").(int64)
		}
		return ShowJSONIterator(stream, maxItems, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "images edit",
			Transform:      transform,
		})
	} else {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.Images.Edit(ctx, params, options...)
		if err != nil {
			return err
		}

		obj := gjson.ParseBytes(res)
		return ShowJSON(obj, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "images edit",
			Transform:      transform,
		})
	}
}

func handleImagesGenerate(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.ImageGenerateParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if streamFlagValue, _ := cmd.Value("stream").(*bool); streamFlagValue != nil && *streamFlagValue {
		stream := client.Images.GenerateStreaming(ctx, params, options...)
		maxItems := int64(-1)
		if cmd.IsSet("max-items") {
			maxItems = cmd.Value("max-items").(int64)
		}
		return ShowJSONIterator(stream, maxItems, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "images generate",
			Transform:      transform,
		})
	} else {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.Images.Generate(ctx, params, options...)
		if err != nil {
			return err
		}

		obj := gjson.ParseBytes(res)
		return ShowJSON(obj, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "images generate",
			Transform:      transform,
		})
	}
}
