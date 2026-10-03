// File generated from our OpenAPI spec by Castiron. See CONTRIBUTING.md for details.

package cmd

import (
	"context"
	"fmt"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-cli/pkg/custom"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

var audioVoicesCreate = cli.Command{
	Name:    "create",
	Usage:   "Creates a voice from a text prompt or from a consent recording and an audio\nsample.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "audio-sample",
			Usage:     "The sample audio recording file. Maximum size is 10 MiB.\n\nSupported MIME types:\n`audio/mpeg`, `audio/wav`, `audio/x-wav`, `audio/ogg`, `audio/aac`, `audio/flac`, `audio/webm`, `audio/mp4`.\n",
			BodyPath:  "audio_sample",
			FileInput: true,
		},
		&requestflag.Flag[string]{
			Name:     "consent",
			Usage:    "The consent recording ID (for example, `cons_1234`).",
			BodyPath: "consent",
		},
		&requestflag.Flag[string]{
			Name:     "name",
			Usage:    "The name of the new voice.",
			Required: true,
			BodyPath: "name",
		},
		&requestflag.Flag[string]{
			Name:     "type",
			Usage:    "The voice creation method. Defaults to `audio_sample` when omitted.",
			Default:  "audio_sample",
			BodyPath: "type",
		},
		&requestflag.Flag[string]{
			Name:     "prompt",
			Usage:    "A description of the desired voice. Must not contain only whitespace.",
			BodyPath: "prompt",
		},
		&requestflag.Flag[any]{
			Name:     "model",
			Usage:    "The voice creation model to use. Defaults to `auto`.",
			Default:  "auto",
			BodyPath: "model",
		},
		&requestflag.Flag[string]{
			Name:     "script-hint",
			Usage:    "Optional text for the voice to speak during creation. If omitted, a script is generated from the prompt. Must not be blank after trimming whitespace; scripts that are too short are rejected.",
			BodyPath: "script_hint",
		},
	},
	Action:          handleAudioVoicesCreate,
	HideHelpCommand: true,
}

func handleAudioVoicesCreate(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()

	if len(unusedArgs) > 0 {
		return fmt.Errorf("Unexpected extra arguments: %v", unusedArgs)
	}

	options, err := custom.AudioVoicesFlagOptions(ctx, cmd)
	if err != nil {
		return err
	}

	var res []byte
	// Voice creation requires a regular API key, never an admin-key fallback.
	options = append([]option.RequestOption{option.WithAdminAPIKey("")}, options...)
	options = append(options, option.WithResponseBodyInto(&res))
	err = client.Post(ctx, "audio/voices", nil, nil, options...)
	if err != nil {
		return err
	}

	obj := gjson.ParseBytes(res)
	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	return ShowJSON(obj, ShowJSONOpts{
		Context:        ctx,
		Operation:      "(resource) audio.voices > (method) create",
		OutputKind:     outputResponse,
		ExplicitFormat: explicitFormat,
		Format:         format,
		RawOutput:      cmd.Root().Bool("raw-output"),
		Title:          "audio:voices create",
		Transform:      transform,
	})
}
