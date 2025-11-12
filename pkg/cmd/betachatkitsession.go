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

var betaChatKitSessionsCreate = requestflag.WithInnerFlags(cli.Command{
	Name:    "create",
	Usage:   "Create a ChatKit session.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:     "user",
			Usage:    "A free-form string that identifies your end user; ensures this Session can access other objects that have the same `user` scope.",
			Required: true,
			BodyPath: "user",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "workflow",
			Usage:    "Workflow reference and overrides applied to the chat session.",
			Required: true,
			BodyPath: "workflow",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "chatkit-configuration",
			Usage:    "Optional per-session configuration settings for ChatKit behavior.",
			BodyPath: "chatkit_configuration",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "expires-after",
			Usage:    "Controls when the session expires relative to an anchor timestamp.",
			BodyPath: "expires_after",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "rate-limits",
			Usage:    "Controls request rate limits for the session.",
			BodyPath: "rate_limits",
		},
	},
	Action:          handleBetaChatKitSessionsCreate,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"workflow": {
		&requestflag.InnerFlag[string]{
			Name:       "workflow.id",
			Usage:      "Identifier for the workflow invoked by the session.",
			InnerField: "id",
		},
		&requestflag.InnerFlag[map[string]any]{
			Name:       "workflow.state-variables",
			Usage:      "State variables forwarded to the workflow. Keys may be up to 64 characters, values must be primitive types, and the map defaults to an empty object.",
			InnerField: "state_variables",
		},
		&requestflag.InnerFlag[map[string]any]{
			Name:       "workflow.tracing",
			Usage:      "Optional tracing overrides for the workflow invocation. When omitted, tracing is enabled by default.",
			InnerField: "tracing",
		},
		&requestflag.InnerFlag[string]{
			Name:       "workflow.version",
			Usage:      "Specific workflow version to run. Defaults to the latest deployed version.",
			InnerField: "version",
		},
	},
	"chatkit-configuration": {
		&requestflag.InnerFlag[map[string]any]{
			Name:       "chatkit-configuration.automatic-thread-titling",
			Usage:      "Configuration for automatic thread titling. When omitted, automatic thread titling is enabled by default.",
			InnerField: "automatic_thread_titling",
		},
		&requestflag.InnerFlag[map[string]any]{
			Name:       "chatkit-configuration.file-upload",
			Usage:      "Configuration for upload enablement and limits. When omitted, uploads are disabled by default (max_files 10, max_file_size 512 MB).",
			InnerField: "file_upload",
		},
		&requestflag.InnerFlag[map[string]any]{
			Name:       "chatkit-configuration.history",
			Usage:      "Configuration for chat history retention. When omitted, history is enabled by default with no limit on recent_threads (null).",
			InnerField: "history",
		},
	},
	"expires-after": {
		&requestflag.InnerFlag[string]{
			Name:       "expires-after.anchor",
			Usage:      "Base timestamp used to calculate expiration. Currently fixed to `created_at`.",
			InnerField: "anchor",
		},
		&requestflag.InnerFlag[int64]{
			Name:       "expires-after.seconds",
			Usage:      "Number of seconds after the anchor when the session expires.",
			InnerField: "seconds",
		},
	},
	"rate-limits": {
		&requestflag.InnerFlag[int64]{
			Name:       "rate-limits.max-requests-per-1-minute",
			Usage:      "Maximum number of requests allowed per minute for the session. Defaults to 10.",
			InnerField: "max_requests_per_1_minute",
		},
	},
})

var betaChatKitSessionsCancel = cli.Command{
	Name:    "cancel",
	Usage:   "Cancel an active ChatKit session and return its most recent metadata.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "session-id",
			Required:  true,
			PathParam: "session_id",
		},
	},
	Action:          handleBetaChatKitSessionsCancel,
	HideHelpCommand: true,
}

func handleBetaChatKitSessionsCreate(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.BetaChatKitSessionNewParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Beta.ChatKit.Sessions.New(ctx, params, options...)
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
		Title:          "beta:chatkit:sessions create",
		Transform:      transform,
	})
}

func handleBetaChatKitSessionsCancel(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("session-id") && len(unusedArgs) > 0 {
		cmd.Set("session-id", unusedArgs[0])
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
	_, err = client.Beta.ChatKit.Sessions.Cancel(ctx, cmd.Value("session-id").(string), options...)
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
		Title:          "beta:chatkit:sessions cancel",
		Transform:      transform,
	})
}
