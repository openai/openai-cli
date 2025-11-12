// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"context"
	"fmt"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/realtime"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

var realtimeClientSecretsCreate = requestflag.WithInnerFlags(cli.Command{
	Name:    "create",
	Usage:   "Create a Realtime client secret with an associated session configuration.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[map[string]any]{
			Name:     "expires-after",
			Usage:    "Configuration for the client secret expiration. Expiration refers to the time after which\na client secret will no longer be valid for creating sessions. The session itself may\ncontinue after that time once started. A secret can be used to create multiple sessions\nuntil it expires.\n",
			BodyPath: "expires_after",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "session",
			Usage:    "Session configuration to use for the client secret. Choose either a realtime\nsession or a transcription session.\n",
			BodyPath: "session",
		},
	},
	Action:          handleRealtimeClientSecretsCreate,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"expires-after": {
		&requestflag.InnerFlag[string]{
			Name:       "expires-after.anchor",
			Usage:      "The anchor point for the client secret expiration, meaning that `seconds` will be added to the `created_at` time of the client secret to produce an expiration timestamp. Only `created_at` is currently supported.\n",
			InnerField: "anchor",
		},
		&requestflag.InnerFlag[int64]{
			Name:       "expires-after.seconds",
			Usage:      "The number of seconds from the anchor point to the expiration. Select a value between `10` and `7200` (2 hours). This default to 600 seconds (10 minutes) if not specified.\n",
			InnerField: "seconds",
		},
	},
})

func handleRealtimeClientSecretsCreate(ctx context.Context, cmd *cli.Command) error {
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

	params := realtime.ClientSecretNewParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Realtime.ClientSecrets.New(ctx, params, options...)
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
		Title:          "realtime:client-secrets create",
		Transform:      transform,
	})
}
