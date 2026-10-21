package app

import (
	"context"
	"os"
	"strings"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/clihelp"
	localcommands "github.com/openai/openai-cli/internal/commands"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func registerFineTuningModelLimits(root *cli.Command) {
	group := root.Command("fine-tuning")
	if group == nil || group.Command("model-limits") != nil {
		return
	}
	group.Commands = append(group.Commands, &cli.Command{
		Name: "model-limits", Usage: "Read organization fine-tuning rate limits.",
		Description: "Uses an ordinary API key. Supports full JSON output and standard output options.",
		Suggest:     true, HideHelpCommand: true,
		Metadata: map[string]any{"help-content": clihelp.Content{
			Examples: []clihelp.Example{{Description: "Read fine-tuning rate limits:", Command: "fine-tuning model-limits"}},
		}},
		Action: handleFineTuningModelLimits,
	})
}

func handleFineTuningModelLimits(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Present() {
		return &localcommands.LocalError{Message: "Fine-tuning model limits takes no positional arguments. Use --help to inspect its options."}
	}
	options, err := FlagOptions(cmd, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, EmptyBody, false)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkFineTuningModelLimitsCredentials(cmd); err != nil {
		return err
	}
	client := openai.NewClient(GetDefaultRequestOptions(cmd)...)
	var response []byte
	// Client.Get permits admin fallback. Clear it before explicit request-header overrides.
	options = append([]option.RequestOption{option.WithAdminAPIKey("")}, options...)
	options = append(options, option.WithResponseBodyInto(&response))
	if err := client.Get(ctx, "fine_tuning/model_limits", nil, nil, options...); err != nil {
		return err
	}
	if !gjson.ValidBytes(response) {
		return &localcommands.LocalError{Message: "Fine-tuning model limits returned an invalid JSON response. Check --base-url, then retry this read."}
	}
	root := cmd.Root()
	return ShowJSON(gjson.ParseBytes(response), ShowJSONOpts{
		Context: ctx, Operation: "(resource) fine-tuning > (method) model-limits", OutputKind: OutputResponse,
		ExplicitFormat: root.IsSet("format"), Format: root.String("format"),
		RawOutput: root.Bool("raw-output"), Transform: root.String("transform"),
		Title: "fine-tuning model-limits", Stdout: root.Writer, Stderr: root.ErrWriter,
	})
}

// Match shared request-header precedence without imposing an admin credential requirement.
func checkFineTuningModelLimitsCredentials(cmd *cli.Command) error {
	missing := &localcommands.LocalError{Message: "API key required for fine-tuning model limits. Set OPENAI_API_KEY or an explicit Authorization header. An admin key cannot replace an API key."}
	headers, err := requestconfig.Headers(cmd)
	if err != nil {
		return err
	}
	if values, ok := headers["Authorization"]; ok {
		if values[0] != "" {
			return nil
		}
		return missing
	}
	key := os.Getenv("OPENAI_API_KEY")
	if cmd.IsSet("api-key") {
		key = cmd.String("api-key")
	}
	if key != "" {
		return nil
	}
	var authorization string
	for line := range strings.SplitSeq(os.Getenv("OPENAI_CUSTOM_HEADERS"), "\n") {
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "Authorization") {
			authorization = strings.TrimSpace(value)
		}
	}
	if authorization != "" {
		return nil
	}
	return missing
}
