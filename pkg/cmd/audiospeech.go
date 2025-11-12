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

var audioSpeechCreate = cli.Command{
	Name:    "create",
	Usage:   "Generates audio from the input text.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:     "input",
			Usage:    "The text to generate audio for. The maximum length is 4096 characters.",
			Required: true,
			BodyPath: "input",
		},
		&requestflag.Flag[string]{
			Name:     "model",
			Usage:    "One of the available [TTS models](https://platform.openai.com/docs/models#tts): `tts-1`, `tts-1-hd`, `gpt-4o-mini-tts`, or `gpt-4o-mini-tts-2025-12-15`.\n",
			Required: true,
			BodyPath: "model",
		},
		&requestflag.Flag[any]{
			Name:     "voice",
			Usage:    "The voice to use when generating the audio. Supported built-in voices are `alloy`, `ash`, `ballad`, `coral`, `echo`, `fable`, `onyx`, `nova`, `sage`, `shimmer`, `verse`, `marin`, and `cedar`. You may also provide a custom voice object with an `id`, for example `{ \"id\": \"voice_1234\" }`. Previews of the voices are available in the [Text to speech guide](https://platform.openai.com/docs/guides/text-to-speech#voice-options).",
			Required: true,
			BodyPath: "voice",
		},
		&requestflag.Flag[string]{
			Name:     "instructions",
			Usage:    "Control the voice of your generated audio with additional instructions. Does not work with `tts-1` or `tts-1-hd`.",
			BodyPath: "instructions",
		},
		&requestflag.Flag[string]{
			Name:     "response-format",
			Usage:    "The format to audio in. Supported formats are `mp3`, `opus`, `aac`, `flac`, `wav`, and `pcm`.",
			Default:  "mp3",
			BodyPath: "response_format",
		},
		&requestflag.Flag[float64]{
			Name:     "speed",
			Usage:    "The speed of the generated audio. Select a value from `0.25` to `4.0`. `1.0` is the default.",
			Default:  1,
			BodyPath: "speed",
		},
		&requestflag.Flag[string]{
			Name:     "stream-format",
			Usage:    "The format to stream the audio in. Supported formats are `sse` and `audio`. `sse` is not supported for `tts-1` or `tts-1-hd`.",
			Default:  "audio",
			BodyPath: "stream_format",
		},
		&requestflag.Flag[string]{
			Name:    "output",
			Aliases: []string{"o"},
			Usage:   "The file where the response contents will be stored. Use the value '-' to force output to stdout.",
		},
	},
	Action:          handleAudioSpeechCreate,
	HideHelpCommand: true,
}

func handleAudioSpeechCreate(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AudioSpeechNewParams{}

	response, err := client.Audio.Speech.New(ctx, params, options...)
	if err != nil {
		return err
	}
	message, err := writeBinaryResponse(response, os.Stdout, cmd.String("output"))
	if message != "" {
		fmt.Println(message)
	}
	return err
}
