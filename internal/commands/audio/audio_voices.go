package audio

import (
	"context"
	"fmt"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// ConfigureVoices keeps voice validation and credential selection outside
// generated handlers. Install before command aliases and presentation wrappers.
func ConfigureVoices(root *cli.Command, policy VoiceOptions) {
	voices := root.Command("audio:voices")
	if voices == nil {
		return
	}
	if create := voices.Command("create"); create != nil {
		if create.Metadata == nil {
			create.Metadata = map[string]any{}
		}
		create.Metadata["help-content"] = clihelp.Content{
			Description: create.Description,
			InputNote: "With --type audio_sample (the default), supply --name, --audio-sample, and --consent.\n" +
				"With --type prompt, supply --name and --prompt. Both methods accept piped JSON or YAML.",
			Examples: []clihelp.Example{{Description: "Create a voice from a description:", Command: `audio voices create --type prompt --name Calm --prompt "Warm and clear."`}},
		}
		create.Action = func(ctx context.Context, cmd *cli.Command) error { return createAudioVoice(ctx, cmd, policy) }
	}
}

func createAudioVoice(ctx context.Context, cmd *cli.Command, policy VoiceOptions) error {
	client := openai.NewClient(policy.DefaultRequestOptions(cmd)...)
	if unusedArgs := cmd.Args().Slice(); len(unusedArgs) > 0 {
		return fmt.Errorf("Unexpected extra arguments: %v", unusedArgs)
	}
	options, err := voiceFlagOptions(ctx, cmd, policy.RequestOptions)
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
	return policy.ShowResponse(ctx, cmd, gjson.ParseBytes(res))
}

// VoiceOptions supplies application request/output policy; every callback is required.
type VoiceOptions struct {
	DefaultRequestOptions func(*cli.Command) []option.RequestOption
	RequestOptions        func(*cli.Command) ([]option.RequestOption, error)
	ShowResponse          func(context.Context, *cli.Command, gjson.Result) error
}
