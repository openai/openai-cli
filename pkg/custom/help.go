package custom

import (
	"strings"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

// ConfigureHelp decorates help without changing API commands or their defaults.
func ConfigureHelp(root *cli.Command, args []string) ([]string, bool, error) {
	orderImageCommands(root)
	configureGlobalFlagDescriptions(root)
	configureHelpGroups(root)
	configureImageHelpContent(root)
	return clihelp.Configure(root, args)
}

// Apply copy to root-owned flags only. A request field with the same name keeps
// its generated description, sources, defaults, and parser behavior.
func configureGlobalFlagDescriptions(root *cli.Command) {
	for _, flag := range root.Flags {
		// Root flags can retain their request flag behind a persistent wrapper.
		if wrapped, ok := flag.(interface {
			RequestFlag() *requestflag.Flag[string]
		}); ok {
			flag = wrapped.RequestFlag()
		}
		if wrapped, ok := flag.(interface{ CLIStringFlag() *cli.StringFlag }); ok {
			flag = wrapped.CLIStringFlag()
		}
		switch flag := flag.(type) {
		case *requestflag.Flag[string]:
			switch flag.Name {
			case "api-key":
				flag.Usage = "Authenticate API requests. Set OPENAI_API_KEY to keep the key out of shell history."
			case "admin-api-key":
				flag.Usage = "Authenticate organization administration requests. Set OPENAI_ADMIN_KEY; a project API key cannot replace an admin key."
			case "organization":
				flag.Usage = "Organization ID to send in the OpenAI-Organization request header."
			case "project":
				flag.Usage = "Project ID to send in the OpenAI-Project request header."
			case "webhook-secret":
				flag.Usage = "Webhook signing secret passed to the SDK. The CLI has no webhook verification command."
			default:
				continue
			}
			// Environment names remain visible, but configured values never appear.
			flag.HideDefault = true
		case *cli.StringFlag:
			switch flag.Name {
			case "base-url":
				flag.Usage = "Send API requests to this endpoint.\nEnv: OPENAI_BASE_URL"
				flag.DefaultText = "https://api.openai.com/v1"
			case "format":
				flag.Usage = "Choose how results are displayed. Format names are case-insensitive.\n" +
					"- auto: readable text, including pipes; --transform or --raw-output selects JSON handling.\n" +
					"- text: readable summary.\n" +
					"- json: full JSON.\n" +
					"- jsonl: one JSON value per line.\n" +
					"- yaml: YAML.\n" +
					"- explore: interactive JSON viewer (JSON when piped).\n" +
					"- pretty: styled JSON.\n" +
					"- raw: unformatted JSON.\n\n" +
					"For paginated lists, raw returns one API page envelope. It differs from --raw-output."
			case "format-error":
				flag.Usage = "Choose how errors are displayed on stderr. Uses the same formats as --format.\n" +
					"Defaults to readable text. Inherits json, jsonl, raw, or yaml from --format unless explicitly set.\n" +
					"Use json for full API error details. With auto, --transform-error selects JSON handling."
			case "transform":
				flag.Usage = "Show part of a JSON result, such as 'id', using GJSON path syntax.\n" +
					"For paginated lists, the path applies to each item, except in the interactive explore viewer.\n" +
					"With --format raw, the path applies to the page.\n" +
					"If the path does not match, keep the original result."
			case "transform-error":
				flag.Usage = "Show part of a JSON error, such as 'message', using GJSON path syntax.\n" +
					"API and local errors use 'message'; streamed errors can use 'error.message'.\n" +
					"If the path does not match, keep the original error."
			case mtlsClientCertFileFlag:
				flag.Usage = "PEM client certificate file for mutual TLS (mTLS). Use with --mtls-client-key-file.\n" +
					"Requires an explicit HTTPS endpoint through --base-url or OPENAI_BASE_URL.\n" +
					"For certificate chains, put the client certificate first, then intermediates."
				flag.HideDefault = true
			case mtlsClientKeyFileFlag:
				flag.Usage = "PEM private key file matching the mTLS client certificate. Use with --mtls-client-cert-file."
				flag.HideDefault = true
			}
		case *cli.BoolFlag:
			switch flag.Name {
			case "raw-output":
				flag.Usage = "Print string results without JSON quotes. Useful with --transform. Does not change error output."
			case "debug":
				flag.Usage = "Write HTTP request and response diagnostics to stderr for troubleshooting. May include sensitive data."
			}
		case *requestHeaderFlag:
			flag.Usage = "Add an HTTP request header as 'Name: Value'.\n" +
				"Repeat for multiple headers; the last value for each header name wins.\n" +
				"Example: -H 'X-Request-Tag: cli-test'.\n" +
				"Env: OPENAI_CUSTOM_HEADERS\n" +
				"Environment headers use one 'Name: Value' header per line. Flags override matching environment headers."
		}
	}
}

func configureImageHelpContent(root *cli.Command) {
	images := root.Command("images")
	if images == nil {
		return
	}
	const localOutput = "This local command supports --format auto or text only.\n" +
		"It cannot use --transform or --raw-output."
	for name, content := range map[string]clihelp.Content{
		"generate": {
			InputNote: "Supply the prompt with --prompt or piped JSON/YAML (key: prompt).\n" +
				"Run without flags to enter it in the interactive picker.",
			Description: "Run without flags to choose settings in an interactive terminal.\n" +
				"After saving, the picker reopens with your settings. Ctrl+C exits.\n\n" +
				"Saves to ~/Downloads/gpt-images/. Use --output-dir to choose an existing folder.\n" +
				"For API data without saving, use --format json without --name or --output-dir.\n" +
				"Size and quality choices depend on the model.",
			Examples: []clihelp.Example{{Description: "Generate and save an image:", Command: `images generate --prompt "A tiny cat" --name cat`}},
		},
		"edit": {
			Description: "Replace photo.png with your image's path and the prompt with your changes.\n" +
				"Keeps your original. Saves to ~/Downloads/gpt-images/.\n" +
				"Use --output-dir to choose an existing folder.\n" +
				"For API data without saving, use --format json without --name or --output-dir.\n" +
				"Size and quality choices depend on the model.",
			Examples: []clihelp.Example{{Description: "Edit and save an image:", Command: `images edit --image "photo.png" --prompt "Make the sky purple" --name purple-sky`}},
		},
		"create-variation": {
			Description: "This endpoint is retired and no longer available. Use images edit with a GPT Image model and a prompt.\n" +
				"Replace photo.png with your image's path. Edits save to ~/Downloads/gpt-images/.\n" +
				"The options below describe the legacy variations contract.",
			Examples: []clihelp.Example{{Description: "Create a variation with images edit:", Command: `images edit --image "photo.png" --prompt "Create a variation of this image" --name variation`}},
		},
		"preview": {
			Description: imagePreviewDetails + "\nReplace photo.png with your saved image's path.\n" +
				"Pipes, CI and terminals without supported graphics or color cannot display previews.\n" +
				"Preview limits: 64 MiB and 16 megapixels. Larger originals are still kept.",
			Examples:          []clihelp.Example{{Description: "View a saved image:", Command: `images preview "photo.png"`}},
			GlobalOptionsNote: localOutput,
		},
		"models": {
			Description: "Shows exact model names and checks metadata visibility with your key.\n" +
				"Checks known image models individually, without loading the full API model list.\n" +
				"No images are generated. Checks stop after 15 seconds, with no retries.\n" +
				"Use --offline to show known names without an API key or access check.\n\n" +
				"The known list comes from this CLI's SDK; it may not include newly released models.\n" +
				"Visibility checks do not guarantee image-generation permissions or quota.\n" +
				"Readable output also works in scripts. Failed checks keep partial results and exit nonzero.",
			Examples: []clihelp.Example{
				{Description: "Check image model visibility:", Command: "images models"},
				{Description: "Use an exact model name:", Command: `images generate --prompt "A tiny orange robot" --model gpt-image-2.5-flare`},
				{Description: "Return JSON for scripts:", Command: "--format json images models"},
			},
		},
	} {
		if command := images.Command(name); command != nil {
			setCompleteHelpContent(command, content)
		}
	}
	if inline := images.Command("inline"); inline != nil {
		setCompleteHelpContent(inline, clihelp.Content{
			Description:       inline.Description,
			Examples:          []clihelp.Example{{Description: "Turn on automatic image previews:", Command: "images inline on"}},
			GlobalOptionsNote: localOutput,
		})
		for _, name := range []string{"on", "off"} {
			if command := inline.Command(name); command != nil {
				setCompleteHelpContent(command, clihelp.Content{
					Description:       command.Description,
					Examples:          []clihelp.Example{{Description: "Remember automatic image previews " + name + ":", Command: "images inline " + name}},
					GlobalOptionsNote: localOutput,
				})
			}
		}
	}
}

// Adapt only known feature pages. The shared renderer supplies flags and the
// current executable invocation without changing runtime command descriptions.
func setCompleteHelpContent(command *cli.Command, content clihelp.Content) {
	if command.Metadata == nil {
		command.Metadata = map[string]any{}
	}
	command.Metadata["help-content"] = content
	command.CustomHelpTemplate = ""
}

func configureHelpGroups(root *cli.Command) {
	groups := []clihelp.FlagGroup{
		{Title: "Authentication", Names: []string{"api-key", "admin-api-key", "webhook-secret", "mtls-client-cert-file", "mtls-client-key-file"}, Owner: root},
		{Title: "Output", Names: []string{"format", "format-error", "transform", "transform-error", "raw-output"}, Owner: root},
		{Title: "Request options", Names: []string{"organization", "project", "base-url", "header"}, Owner: root},
		{Title: "Troubleshooting", Names: []string{"debug"}, Owner: root},
	}
	labels := []clihelp.FlagLabel{
		{Owner: root, Names: []string{"api-key", "admin-api-key"}, Label: "KEY"},
		{Owner: root, Names: []string{"organization", "project"}, Label: "ID"},
		{Owner: root, Names: []string{"webhook-secret"}, Label: "SECRET"},
		{Owner: root, Names: []string{"header"}, Label: "'NAME: VALUE'"},
	}
	var visit func(*cli.Command, bool)
	visit = func(command *cli.Command, image bool) {
		if command.Metadata == nil {
			command.Metadata = map[string]any{}
		}
		sections := append([]clihelp.FlagGroup(nil), groups...)
		if image {
			sections = append([]clihelp.FlagGroup{
				{Title: "Image settings", Names: []string{"model", "n", "size", "quality", "background", "moderation", "input-fidelity", "mask"}},
				{Title: "Image file format", Names: []string{"output-format", "output-compression"}},
				{Title: "Saving", Names: []string{"output-dir", "name"}},
				{Title: "Terminal previews", Names: []string{"inline"}},
				{Title: "Progress", Names: []string{"stream", "partial-images", "max-items"}},
				{Title: "API response", Names: []string{"response-format"}},
			}, sections...)
		}
		command.Metadata["help-flag-groups"] = sections
		commandLabels := append([]clihelp.FlagLabel(nil), labels...)
		if positional, ok := command.Metadata["help-positional-flags"].([]string); ok {
			for _, name := range positional {
				commandLabels = append(commandLabels, clihelp.FlagLabel{
					Owner: command, Names: []string{name}, Label: strings.ToUpper(strings.ReplaceAll(name, "-", "_")),
				})
			}
		}
		if image {
			for _, label := range []struct{ name, value string }{
				{"size", "SIZE"}, {"quality", "QUALITY"}, {"background", "BACKGROUND"},
				{"moderation", "LEVEL"}, {"input-fidelity", "FIDELITY"},
				{"output-format", "FORMAT"}, {"response-format", "FORMAT"},
				{"name", "NAME"}, {"inline", "MODE"},
			} {
				commandLabels = append(commandLabels, clihelp.FlagLabel{Owner: command, Names: []string{label.name}, Label: label.value})
			}
		}
		command.Metadata["help-flag-labels"] = commandLabels
		for _, flag := range command.Flags {
			if limit, ok := flag.(*requestflag.Flag[int64]); ok && limit.Name == "max-items" &&
				limit.Usage == "The maximum number of items to return (use -1 for unlimited)." {
				// Generated handlers distinguish omission from an explicit zero.
				limit.DefaultText = "unlimited"
				limit.Usage = "Maximum items to return; omit or use -1 for unlimited, or use 0 for no items."
			}
		}
		for _, child := range command.Commands {
			visit(child, image || child == root.Command("images"))
		}
	}
	visit(root, false)
}
