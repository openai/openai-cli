// File generated from our OpenAPI spec by Castiron. See CONTRIBUTING.md for details.

package commands

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

var audioVoiceConsentsCreate = cli.Command{
	Name:    "create",
	Usage:   "Upload a voice consent recording.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:     "language",
			Usage:    "The BCP 47 language tag for the consent phrase (for example, `en-US`).",
			Required: true,
			BodyPath: "language",
		},
		&requestflag.Flag[string]{
			Name:     "name",
			Usage:    "The label to use for this consent recording.",
			Required: true,
			BodyPath: "name",
		},
		&requestflag.Flag[string]{
			Name:      "recording",
			Usage:     "The consent audio recording file. Maximum size is 10 MiB.\n\nSupported MIME types:\n`audio/mpeg`, `audio/wav`, `audio/x-wav`, `audio/ogg`, `audio/aac`, `audio/flac`, `audio/webm`, `audio/mp4`.\n",
			Required:  true,
			BodyPath:  "recording",
			FileInput: true,
		},
	},
	Action:          handleAudioVoiceConsentsCreate,
	HideHelpCommand: true,
}

var audioVoiceConsentsRetrieve = cli.Command{
	Name:    "retrieve",
	Usage:   "Retrieves a voice consent recording.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "consent-id",
			Required:  true,
			PathParam: "consent_id",
		},
	},
	Action:          handleAudioVoiceConsentsRetrieve,
	HideHelpCommand: true,
}

var audioVoiceConsentsUpdate = cli.Command{
	Name:    "update",
	Usage:   "Updates a voice consent recording (metadata only).",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "consent-id",
			Required:  true,
			PathParam: "consent_id",
		},
		&requestflag.Flag[string]{
			Name:     "name",
			Usage:    "The updated label for this consent recording.",
			Required: true,
			BodyPath: "name",
		},
	},
	Action:          handleAudioVoiceConsentsUpdate,
	HideHelpCommand: true,
}

var audioVoiceConsentsList = cli.Command{
	Name:    "list",
	Usage:   "Returns a list of voice consent recordings.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "after",
			Usage:     "A cursor for use in pagination. `after` is an object ID that defines your place in the list. For instance, if you make a list request and receive 100 objects, ending with obj_foo, your subsequent call can include after=obj_foo in order to fetch the next page of the list.\n",
			QueryPath: "after",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "A limit on the number of objects to be returned. Limit can range between 1 and 100, and the default is 20.\n",
			Default:   20,
			QueryPath: "limit",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleAudioVoiceConsentsList,
	HideHelpCommand: true,
}

var audioVoiceConsentsDelete = cli.Command{
	Name:    "delete",
	Usage:   "Deletes a voice consent recording.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "consent-id",
			Required:  true,
			PathParam: "consent_id",
		},
	},
	Action:          handleAudioVoiceConsentsDelete,
	HideHelpCommand: true,
}

func handleAudioVoiceConsentsCreate(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AudioVoiceConsentNewParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Audio.VoiceConsents.New(ctx, params, options...)
	if err != nil {
		return err
	}

	obj := gjson.ParseBytes(res)
	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	return ShowJSON(obj, ShowJSONOpts{
		Context:        ctx,
		Operation:      "(resource) audio.voice_consents > (method) create",
		OutputKind:     outputResponse,
		ExplicitFormat: explicitFormat,
		Format:         format,
		RawOutput:      cmd.Root().Bool("raw-output"),
		Title:          "audio:voice-consents create",
		Transform:      transform,
	})
}

func handleAudioVoiceConsentsRetrieve(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("consent-id") && len(unusedArgs) > 0 {
		cmd.Set("consent-id", unusedArgs[0])
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
	_, err = client.Audio.VoiceConsents.Get(ctx, cmd.Value("consent-id").(string), options...)
	if err != nil {
		return err
	}

	obj := gjson.ParseBytes(res)
	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	return ShowJSON(obj, ShowJSONOpts{
		Context:        ctx,
		Operation:      "(resource) audio.voice_consents > (method) retrieve",
		OutputKind:     outputResponse,
		ExplicitFormat: explicitFormat,
		Format:         format,
		RawOutput:      cmd.Root().Bool("raw-output"),
		Title:          "audio:voice-consents retrieve",
		Transform:      transform,
	})
}

func handleAudioVoiceConsentsUpdate(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("consent-id") && len(unusedArgs) > 0 {
		cmd.Set("consent-id", unusedArgs[0])
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

	params := openai.AudioVoiceConsentUpdateParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Audio.VoiceConsents.Update(
		ctx,
		cmd.Value("consent-id").(string),
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
		Context:        ctx,
		Operation:      "(resource) audio.voice_consents > (method) update",
		OutputKind:     outputResponse,
		ExplicitFormat: explicitFormat,
		Format:         format,
		RawOutput:      cmd.Root().Bool("raw-output"),
		Title:          "audio:voice-consents update",
		Transform:      transform,
	})
}

func handleAudioVoiceConsentsList(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AudioVoiceConsentListParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if format == "raw" {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.Audio.VoiceConsents.List(ctx, params, options...)
		if err != nil {
			return err
		}
		obj := gjson.ParseBytes(res)
		return ShowJSON(obj, ShowJSONOpts{
			Context:        ctx,
			Operation:      "(resource) audio.voice_consents > (method) list",
			OutputKind:     outputResponse,
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "audio:voice-consents list",
			Transform:      transform,
		})
	} else {
		iter := client.Audio.VoiceConsents.ListAutoPaging(ctx, params, options...)
		maxItems := int64(-1)
		if cmd.IsSet("max-items") {
			maxItems = cmd.Value("max-items").(int64)
		}
		return ShowJSONIterator(iter, maxItems, ShowJSONOpts{
			Context:        ctx,
			Operation:      "(resource) audio.voice_consents > (method) list",
			OutputKind:     outputPageItem,
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "audio:voice-consents list",
			Transform:      transform,
		})
	}
}

func handleAudioVoiceConsentsDelete(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("consent-id") && len(unusedArgs) > 0 {
		cmd.Set("consent-id", unusedArgs[0])
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
	_, err = client.Audio.VoiceConsents.Delete(ctx, cmd.Value("consent-id").(string), options...)
	if err != nil {
		return err
	}

	obj := gjson.ParseBytes(res)
	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	return ShowJSON(obj, ShowJSONOpts{
		Context:        ctx,
		Operation:      "(resource) audio.voice_consents > (method) delete",
		OutputKind:     outputResponse,
		ExplicitFormat: explicitFormat,
		Format:         format,
		RawOutput:      cmd.Root().Bool("raw-output"),
		Title:          "audio:voice-consents delete",
		Transform:      transform,
	})
}
