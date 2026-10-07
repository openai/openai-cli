package custom

import (
	"slices"
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
	configureImageHelpInvocation(root, clihelp.Invocation(root.Name, args))
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
				flag.Usage = "Send API requests to this endpoint. Env: OPENAI_BASE_URL."
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
				"Env: OPENAI_CUSTOM_HEADERS accepts one 'Name: Value' header per line. Flags override matching environment headers."
		}
	}
}

func configureImageHelpInvocation(root *cli.Command, invocation string) {
	images := root.Command("images")
	if images == nil {
		return
	}
	for name, description := range map[string]string{
		"generate":         imageGenerationSavingHelp,
		"edit":             imageEditSavingHelp,
		"create-variation": imageVariationSavingHelp,
	} {
		if command := images.Command(name); command != nil {
			// Descriptions are plain text, not templates. Start from the original
			// each time so repeated help configuration cannot retain an old path.
			command.Description = strings.Replace(description, "\n    openai ", "\n    "+invocation+" ", 1)
		}
	}
}

func configureHelpGroups(root *cli.Command) {
	groups := []clihelp.FlagGroup{
		{Title: "Authentication", Names: []string{"api-key", "admin-api-key", "webhook-secret", "mtls-client-cert-file", "mtls-client-key-file"}, Owner: root},
		{Title: "Output", Names: []string{"format", "format-error", "transform", "transform-error", "raw-output"}, Owner: root},
		{Title: "Request options", Names: []string{"organization", "project", "base-url", "header"}, Owner: root},
		{Title: "Troubleshooting", Names: []string{"debug"}, Owner: root},
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
	// These API commands support every output option shown in their brief help.
	for _, path := range [][]string{{"models", "list"}, {"models", "retrieve"}, {"responses", "create"}, {"responses", "retrieve"}} {
		command := root
		var local []cli.Flag
		for _, name := range path {
			command = command.Command(name)
			if command == nil {
				break
			}
			local = append(local, command.Flags...)
		}
		if command != nil {
			var output []cli.Flag
			for _, name := range []string{"format", "transform", "raw-output"} {
				for _, flag := range root.Flags {
					shadowed := slices.ContainsFunc(local, func(other cli.Flag) bool {
						return slices.ContainsFunc(other.Names(), func(alias string) bool {
							return slices.Contains(flag.Names(), alias)
						})
					})
					if len(flag.Names()) > 0 && flag.Names()[0] == name && !shadowed {
						output = append(output, flag)
					}
				}
			}
			command.Metadata["brief-output-flags"] = output
		}
	}
}
