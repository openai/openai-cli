package custom

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/imagemodels"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-go/v3"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

const imageModelsHelp = `{{$bin := or (index .Root.Metadata "help-invocation") "openai"}}Find an image model
  {{$bin}} images models

Shows exact model names and checks metadata visibility with your key.
Checks known image models individually, without loading the full API model list.
No images are generated. Checks stop after 15 seconds, with no retries.

  --all       Include dated versions, retired models and models not visible to your key
  --offline   Show known names immediately, without an API key or access check

Use an exact name from the list:
  {{$bin}} images generate --prompt "A tiny orange robot" --model gpt-image-2.5-flare

The known list comes from this CLI's SDK; it may not include newly released models.
Visibility checks do not guarantee image-generation permissions or quota.
JSON for scripts: {{$bin}} --format json images models
Readable output also works in scripts. Failed checks keep partial results and exit nonzero.
API key setup: {{$bin}} help setup
`

// Image-model discovery is a CLI workflow over the existing model retrieval
// operation. Keep generated /models listing and its script contract untouched.
func registerImageModels(root *cli.Command) {
	for _, resource := range root.Commands {
		if resource.Name == "images" {
			resource.Commands = append(resource.Commands, &cli.Command{
				Name: "models", Usage: "See exact image model names and check visibility with your key",
				Description:        "Checks known image models individually. Metadata visibility does not guarantee generation permissions. No images are generated.",
				CustomHelpTemplate: imageModelsHelp, HideHelpCommand: true, Suggest: true,
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "all", Usage: "Include known snapshots, retired models and models not visible to your key", HideDefault: true},
					&cli.BoolFlag{Name: "offline", Usage: "Show known names without an API request or access check", HideDefault: true},
				},
				Action: handleImagesModels,
			})
			return
		}
	}
}

type imageModelsReport struct {
	Source   string               `json:"source"`
	Complete bool                 `json:"complete"`
	Models   []imagemodels.Result `json:"models"`
}

// Keep discovery's local guidance identifiable without retaining API errors or
// rejected arguments. Only failure categories and the local help invocation
// contribute to its message.
type imageModelsError struct {
	results        []imagemodels.Result
	invocation     string
	extraArguments bool
}

func (e *imageModelsError) Error() string {
	if e.extraArguments {
		return fmt.Sprintf("use %s images models to see image model names; no additional arguments are needed", e.invocation)
	}
	return imageModelsFailureMessage(e.results, e.invocation)
}

func handleImagesModels(ctx context.Context, command *cli.Command) error {
	invocation := imageModelsInvocation(command)
	if command.Args().Present() {
		return &imageModelsError{invocation: invocation, extraArguments: true}
	}
	root := command.Root()
	human := root.String("transform") == "" && !root.Bool("raw-output") &&
		resolvedOutputFormat(ShowJSONOpts{Format: root.String("format")}) == "text"
	report := imageModelsReport{Source: "live", Complete: true}
	var results []imagemodels.Result
	if command.Bool("offline") {
		report.Source, report.Complete = "offline", false
		for _, entry := range imagemodels.Catalog(command.Bool("all")) {
			results = append(results, imagemodels.Result{Entry: entry, Status: imagemodels.StatusNotChecked})
		}
	} else {
		// Parse request options once before parallel lookups, retaining normal
		// headers, authentication, project, endpoint and mTLS handling. Discovery
		// has no request body or stdin parameters.
		options, err := FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, EmptyBody, true)
		if err != nil {
			return err
		}
		client := openai.NewClient(GetDefaultRequestOptions(command)...)
		if human && isTerminal(os.Stderr) && !root.Bool("debug") {
			if _, err := fmt.Fprintln(os.Stderr, "Checking image models..."); err != nil {
				return err
			}
		}
		interruptContext, stop := signal.NotifyContext(ctx, os.Interrupt)
		defer stop()
		checkContext, cancel := context.WithTimeout(interruptContext, 15*time.Second)
		results = imagemodels.Discover(checkContext, &client.Models, command.Bool("all"), options...)
		cancel()
	}
	for _, result := range results {
		report.Models = append(report.Models, result)
		if result.Status == imagemodels.StatusUnknown {
			report.Complete = false
		}
	}
	if human {
		if err := writeImageModels(root.Writer, report, command.Bool("all"), invocation); err != nil {
			return err
		}
	} else {
		if err := writeImageModelsData(command, report); err != nil {
			return err
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if report.Source == "live" && !report.Complete {
		return &imageModelsError{results: results, invocation: invocation}
	}
	return nil
}

func writeImageModelsData(command *cli.Command, report imageModelsReport) error {
	root := command.Root()
	payload, err := json.Marshal(report)
	if err != nil {
		return err
	}
	obj := gjson.ParseBytes(payload)
	opts := ShowJSONOpts{
		Format: root.String("format"), ExplicitFormat: root.IsSet("format"),
		RawOutput: root.Bool("raw-output"), Title: "Image models", Transform: root.String("transform"),
	}
	// The report includes completed checks even after discovery is canceled.
	// Presentation therefore uses its own context instead of the canceled lookup.
	opts.Stdout, opts.Stderr = root.Writer, root.ErrWriter
	return ShowJSON(obj, opts)
}

func writeImageModels(out io.Writer, report imageModelsReport, all bool, invocation string) error {
	var text strings.Builder
	if report.Source == "offline" {
		text.WriteString("Known image models — access not checked (offline)\n\n")
	} else {
		text.WriteString("Image models — checked with your API key\n\n")
	}
	table := tabwriter.NewWriter(&text, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "MODEL\tSTATUS")
	hidden, shown := 0, 0
	choice := ""
	for _, model := range report.Models {
		if !all && (model.Status == imagemodels.StatusNotVisible || model.Status == imagemodels.StatusRetired) {
			hidden++
			continue
		}
		fmt.Fprintf(table, "%s\t%s\n", model.ID, imageModelStatusText(model))
		shown++
		if model.Status == imagemodels.StatusVisible || model.Status == imagemodels.StatusNotChecked {
			if choice == "" {
				choice = model.ID
			}
		}
	}
	if err := table.Flush(); err != nil {
		return err
	}
	if shown == 0 {
		text.WriteString("No known, active image models were visible to this key.\n")
	}
	if hidden > 0 {
		fmt.Fprintf(&text, "\n%d retired or not visible. Include them and dated versions: %s images models --all\n", hidden, invocation)
	}
	if choice != "" {
		fmt.Fprintf(&text, "\nChoose a model:\n  %s images generate --prompt \"A tiny orange robot\" --model %s\n", invocation, choice)
	}

	text.WriteString("\nKnown image IDs from this CLI; newly released models may not be listed.\n")
	if report.Source == "live" {
		text.WriteString("Visible means model information was accessible; generation permissions can differ.\n")
	}
	return readable.WriteText(out, text.String())
}

func imageModelStatusText(model imagemodels.Result) string {
	switch model.Status {
	case imagemodels.StatusVisible:
		if model.ShutdownDate != "" {
			return "Visible (retires " + model.ShutdownDate + ")"
		}
		return "Visible"
	case imagemodels.StatusNotVisible:
		return "Not visible to this key"
	case imagemodels.StatusRetired:
		return "Retired " + model.ShutdownDate
	case imagemodels.StatusNotChecked:
		return "Not checked"
	default:
		switch model.Failure {
		case imagemodels.FailureAuthentication:
			return "Could not check (authentication)"
		case imagemodels.FailureForbidden:
			return "Could not check (access denied)"
		case imagemodels.FailureRateLimit:
			return "Could not check (rate limit)"
		case imagemodels.FailureTimeout:
			return "Could not check (timeout)"
		default:
			return "Could not check"
		}
	}
}

func imageModelsFailureMessage(results []imagemodels.Result, invocation string) string {
	failures := make(map[imagemodels.Failure]bool)
	for _, result := range results {
		if result.Status == imagemodels.StatusUnknown {
			failures[result.Failure] = true
		}
	}
	message := "Some model checks could not be completed. Unchecked models have not been verified."
	switch {
	case failures[imagemodels.FailureAuthentication]:
		message = "The API did not accept authentication for a model check. Check your key or endpoint credentials.\nAPI key setup: " + invocation + " help setup"
	case failures[imagemodels.FailureRateLimit]:
		message = "The API rate-limited model checks. Wait briefly before trying again."
	case failures[imagemodels.FailureTimeout] || failures[imagemodels.FailureServer]:
		message = "Some model checks timed out or the API could not complete them. Try again shortly."
	case failures[imagemodels.FailureForbidden]:
		message = "The API denied access to one or more model checks. Check your key's permissions."
	case failures[imagemodels.FailureNetwork]:
		message = "Could not reach the API for every model check. Check your connection and try again."
	case failures[imagemodels.FailureInvalidResponse]:
		message = "The API returned an unexpected model response. Those models have not been verified."
	}
	return message + "\nBrowse known names without an API check: " + invocation + " images models --offline"
}

// Use the help system's already quoted executable name for runnable examples.
func imageModelsInvocation(command *cli.Command) string {
	invocation, _ := command.Root().Metadata["help-invocation"].(string)
	if invocation == "" {
		return "openai"
	}
	return invocation
}
