package custom

import (
	"strings"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/urfave/cli/v3"
)

const fineTuningAvailabilityURL = "https://developers.openai.com/api/docs/deprecations#update-to-openais-self-serve-fine-tuning"

// Called only after the readable iterator finishes without an error or records.
func fineTuningEmptyResult(opts ShowJSONOpts) (string, bool) {
	if opts.Operation != "(resource) fine_tuning.jobs > (method) list" ||
		opts.OutputKind != OutputPageItem || opts.Transform != "" || opts.RawOutput {
		return "", false
	}
	return "No fine-tuning jobs returned.\nTraining eligibility was not checked.", true
}

// Help runs after subgroup cloning. Visit both spaced and canonical colon routes.
func configureFineTuningHelpContent(root *cli.Command) {
	const availability = "Training access is restricted. Listing jobs does not check training eligibility.\n" +
		"Current availability and dates: " + fineTuningAvailabilityURL
	const sharing = "Checkpoint permissions share model access across projects in the same organization.\n" +
		"These operations require an admin API key. They do not download model weights.\n" +
		"The CLI has no checkpoint export command."
	content := map[string]clihelp.Content{
		"fine-tuning": {
			Description: availability,
			Examples:    []clihelp.Example{{Description: "List existing jobs:", Command: "fine-tuning jobs list"}},
		},
		"fine-tuning jobs": {
			Description: "Use retrieve for job status and list-events for progress or failure details.\n" + availability,
			Examples:    []clihelp.Example{{Description: "Inspect a job's events:", Command: "fine-tuning jobs list-events --fine-tuning-job-id ftjob_example"}},
		},
		"fine-tuning jobs list": {
			Description: "An empty result means this request returned no jobs. It does not establish training eligibility.\n" +
				"Check your project context, metadata filters, and --after cursor if you expected an existing job.",
			Examples: []clihelp.Example{{Description: "List existing jobs:", Command: "fine-tuning jobs list"}},
		},
		"fine-tuning jobs retrieve": {
			Description: "Inspect status and error details before choosing a recovery action.\n" +
				"Use list-events with the same job ID for progress messages. Use --format json for full API fields.",
			Examples: []clihelp.Example{{Description: "Inspect an existing job:", Command: "fine-tuning jobs retrieve --fine-tuning-job-id ftjob_example"}},
		},
		"fine-tuning jobs list-events": {
			Description: "Lists recorded progress and failure events. This command does not wait or follow future events.",
			Examples:    []clihelp.Example{{Description: "Read a job's events:", Command: "fine-tuning jobs list-events --fine-tuning-job-id ftjob_example"}},
		},
		"fine-tuning jobs pause": {
			Description: "Pause a supported running job. The API decides whether its current state allows pausing.\n" +
				"Inspect the returned status; a successful request does not mean training completed.",
		},
		"fine-tuning jobs resume": {
			Description: "Resume a supported paused job. Resuming continues training and can incur charges.\n" +
				"The API decides whether the job can resume. Inspect the returned status.",
		},
		"fine-tuning jobs create": {Description: availability},
		"fine-tuning jobs checkpoints": {
			Description: "List checkpoint metadata for a job. This command does not download model weights.",
			Examples:    []clihelp.Example{{Description: "List a job's checkpoints:", Command: "fine-tuning jobs checkpoints list --fine-tuning-job-id ftjob_example"}},
		},
		"fine-tuning checkpoints":             {Description: sharing},
		"fine-tuning checkpoints permissions": {Description: sharing},
	}
	var visit func(*cli.Command, string)
	visit = func(command *cli.Command, path string) {
		if guidance, ok := content[path]; ok {
			if command.Metadata == nil {
				command.Metadata = map[string]any{}
			}
			if command.Description != "" {
				guidance.Description = command.Description + "\n\n" + guidance.Description
			}
			command.Metadata["help-content"] = guidance
		}
		for _, child := range command.Commands {
			visit(child, strings.TrimSpace(path+" "+strings.ReplaceAll(child.Name, ":", " ")))
		}
	}
	visit(root, "")
}
