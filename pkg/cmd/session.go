// File generated from our OpenAPI spec by Castiron. See CONTRIBUTING.md for details.

package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/live"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

var liveSessionsAccept = requestflag.WithInnerFlags(cli.Command{
	Name:    "accept",
	Usage:   "Accept an incoming SIP call with Live startup configuration.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "session-id",
			Required:  true,
			PathParam: "session_id",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "session",
			Usage:    "Model and startup configuration for the Live session that answers the incoming SIP call.",
			Required: true,
			BodyPath: "session",
		},
	},
	Action:          handleLiveSessionsAccept,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"session": {
		&requestflag.InnerFlag[string]{
			Name:       "session.model",
			Usage:      "The Live model to use for the accepted call.",
			InnerField: "model",
		},
		&requestflag.InnerFlag[string]{
			Name:       "session.type",
			Usage:      "The session type. Always `live`.",
			InnerField: "type",
		},
		&requestflag.InnerFlag[map[string]any]{
			Name:       "session.audio",
			Usage:      "Startup audio output configuration. SIP negotiates the media format; audio.format is only accepted for primary WebSockets. Voice cannot change after startup.",
			InnerField: "audio",
		},
		&requestflag.InnerFlag[map[string]any]{
			Name:       "session.delegation",
			Usage:      "Who handles tasks delegated by the Live model. Omitted or null selects your application; use `responses` to let the API manage a Responses backend.",
			InnerField: "delegation",
		},
		&requestflag.InnerFlag[[]map[string]any]{
			Name:       "session.input",
			Usage:      "Ordered text-only history supplied before startup. Supports developer, user, and assistant messages with one text part each; at most 128 messages and 8,192 rendered tokens in total.",
			InnerField: "input",
		},
		&requestflag.InnerFlag[*string]{
			Name:       "session.instructions",
			Usage:      "Frontend instructions for voice, conversation, interruptions, and when to delegate. Start with the [Live prompting guide](https://developers.openai.com/api/docs/guides/live-prompting); put business rules and tool workflows in a separate [backend prompt](https://developers.openai.com/api/docs/guides/live-delegation#start-with-your-existing-backend-prompt). Limited to 16,384 client-supplied tokens. Omitted or blank instructions use server defaults. Immutable after startup.",
			InnerField: "instructions",
		},
		&requestflag.InnerFlag[bool]{
			Name:       "session.store",
			Usage:      "Whether to store the session for later forking and recording download. Defaults to false for new sessions.",
			InnerField: "store",
		},
	},
})

var liveSessionsDownloadRecording = cli.Command{
	Name:    "download-recording",
	Usage:   "Get Live session content",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "session-id",
			Usage:     "The ID of the stored Live session to download. Use the session ID returned when the session started with storage enabled.",
			Required:  true,
			PathParam: "session_id",
		},
		&requestflag.Flag[string]{
			Name:    "output",
			Aliases: []string{"o"},
			Usage:   "The file where the response contents will be stored. Use the value '-' to force output to stdout.",
		},
	},
	Action:          handleLiveSessionsDownloadRecording,
	HideHelpCommand: true,
}

var liveSessionsFork = requestflag.WithInnerFlags(cli.Command{
	Name:    "fork",
	Usage:   "Fork a stored Live session onto a new WebRTC connection.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "session-id",
			Required:  true,
			PathParam: "session_id",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "transport",
			Usage:    "WebRTC transport with an SDP offer for the new connection to the forked session.",
			Required: true,
			BodyPath: "transport",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "session",
			Usage:    "Optional overrides for a stored Live session. Omitted settings are inherited. The model, voice, frontend instructions, and prior conversation come from the stored session. WebRTC negotiates its audio format; audio.format is only supported on WebSocket forks.",
			BodyPath: "session",
		},
	},
	Action:          handleLiveSessionsFork,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"transport": {
		&requestflag.InnerFlag[string]{
			Name:       "transport.sdp",
			Usage:      "Session Description Protocol message for the WebRTC connection.",
			InnerField: "sdp",
		},
		&requestflag.InnerFlag[string]{
			Name:       "transport.type",
			Usage:      "The transport used for the Live session. Always `webrtc`.",
			InnerField: "type",
		},
	},
	"session": {
		&requestflag.InnerFlag[map[string]any]{
			Name:       "session.client",
			Usage:      "Startup-only capabilities for an untrusted frontend attached to a unified WebRTC session. Trusted sideband connections are unaffected.",
			InnerField: "client",
		},
		&requestflag.InnerFlag[map[string]any]{
			Name:       "session.delegation",
			Usage:      "Update the Responses backend for an existing Live session without changing delegation ownership.",
			InnerField: "delegation",
		},
		&requestflag.InnerFlag[bool]{
			Name:       "session.store",
			Usage:      "Whether to store the forked session. Omission inherits the stored session's setting.",
			InnerField: "store",
		},
	},
})

var liveSessionsHangup = cli.Command{
	Name:    "hangup",
	Usage:   "Hang up a Live session.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "session-id",
			Required:  true,
			PathParam: "session_id",
		},
	},
	Action:          handleLiveSessionsHangup,
	HideHelpCommand: true,
}

var liveSessionsRefer = cli.Command{
	Name:    "refer",
	Usage:   "Transfer a Live SIP call to another destination.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "session-id",
			Required:  true,
			PathParam: "session_id",
		},
		&requestflag.Flag[string]{
			Name:     "target-uri",
			Usage:    "Nonblank URI for the SIP Refer-To header, such as tel:+14155550123 or sip:agent@example.com.",
			Required: true,
			BodyPath: "target_uri",
		},
	},
	Action:          handleLiveSessionsRefer,
	HideHelpCommand: true,
}

var liveSessionsReject = cli.Command{
	Name:    "reject",
	Usage:   "Reject an incoming SIP call.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "session-id",
			Required:  true,
			PathParam: "session_id",
		},
		&requestflag.Flag[int64]{
			Name:     "status-code",
			Usage:    "SIP rejection status sent to the caller. This field is required.",
			Required: true,
			BodyPath: "status_code",
		},
	},
	Action:          handleLiveSessionsReject,
	HideHelpCommand: true,
}

func handleLiveSessionsAccept(ctx context.Context, cmd *cli.Command) error {
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
		ApplicationJSON,
		false,
	)
	if err != nil {
		return err
	}

	params := live.SessionAcceptParams{}

	return client.Live.Sessions.Accept(
		ctx,
		cmd.Value("session-id").(string),
		params,
		options...,
	)
}

func handleLiveSessionsDownloadRecording(ctx context.Context, cmd *cli.Command) error {
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

	response, err := client.Live.Sessions.DownloadRecording(ctx, cmd.Value("session-id").(string), options...)
	if err != nil {
		return err
	}
	message, err := writeBinaryResponse(response, os.Stdout, cmd.String("output"))
	if message != "" {
		fmt.Println(message)
	}
	return err
}

func handleLiveSessionsFork(ctx context.Context, cmd *cli.Command) error {
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
		ApplicationJSON,
		false,
	)
	if err != nil {
		return err
	}

	params := live.SessionForkParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Live.Sessions.Fork(
		ctx,
		cmd.Value("session-id").(string),
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
		Title:          "live:sessions fork",
		Transform:      transform,
	})
}

func handleLiveSessionsHangup(ctx context.Context, cmd *cli.Command) error {
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

	return client.Live.Sessions.Hangup(ctx, cmd.Value("session-id").(string), options...)
}

func handleLiveSessionsRefer(ctx context.Context, cmd *cli.Command) error {
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
		ApplicationJSON,
		false,
	)
	if err != nil {
		return err
	}

	params := live.SessionReferParams{}

	return client.Live.Sessions.Refer(
		ctx,
		cmd.Value("session-id").(string),
		params,
		options...,
	)
}

func handleLiveSessionsReject(ctx context.Context, cmd *cli.Command) error {
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
		ApplicationJSON,
		false,
	)
	if err != nil {
		return err
	}

	params := live.SessionRejectParams{}

	return client.Live.Sessions.Reject(
		ctx,
		cmd.Value("session-id").(string),
		params,
		options...,
	)
}
