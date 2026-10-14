package custom

import (
	"context"
	"fmt"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// Keep the CLI's sample-only voice validation and credential selection outside
// generated handlers. Install before command aliases and presentation wrappers.
func configureAudioVoices(root *cli.Command) {
	voices := root.Command("audio:voices")
	if voices == nil {
		return
	}
	if create := voices.Command("create"); create != nil {
		create.Action = createAudioVoice
	}
}

func createAudioVoice(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(GetDefaultRequestOptions(cmd)...)
	if unusedArgs := cmd.Args().Slice(); len(unusedArgs) > 0 {
		return fmt.Errorf("Unexpected extra arguments: %v", unusedArgs)
	}
	options, err := AudioVoicesFlagOptions(ctx, cmd)
	if err != nil {
		return err
	}

	var res []byte
	// Voice creation requires a regular API key, never an admin-key fallback.
	options = append([]option.RequestOption{option.WithAdminAPIKey("")}, options...)
	options = append(options, option.WithResponseBodyInto(&res))
	if err := client.Post(ctx, "audio/voices", nil, nil, options...); err != nil {
		return err
	}
	return ShowJSON(gjson.ParseBytes(res), ShowJSONOpts{
		Context:        ctx,
		Operation:      "(resource) audio.voices > (method) create",
		OutputKind:     OutputResponse,
		ExplicitFormat: cmd.Root().IsSet("format"),
		Format:         cmd.Root().String("format"),
		RawOutput:      cmd.Root().Bool("raw-output"),
		Title:          "audio:voices create",
		Transform:      cmd.Root().String("transform"),
	})
}
