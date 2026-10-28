package app

import (
	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/urfave/cli/v3"
)

// Batch examples feed both command help and the existing offline command search.
func configureBatchHelpContent(root *cli.Command) {
	batches := root.Command("batches")
	if batches == nil {
		return
	}
	group := clihelp.Content{
		Description: "Start a new batch with submit. Inspect existing batches with list.\n" +
			"A batch processes the requests in a JSONL file. Each request needs a unique custom_id.",
		Notes: "Help does not contact the API. Batch operations use your configured API key and project.\n" +
			"Use --help after any command for its options and examples.",
	}
	if batches.Command("browse") != nil {
		group.Examples = append(group.Examples, clihelp.Example{
			Description: "Choose a batch and open its status, results, errors, or downloads:", Command: "batches browse",
		})
	}
	if root.Command("explore") != nil {
		group.Examples = append(group.Examples, clihelp.Example{
			Description: "Find Batch commands and examples without an API request:", Command: "explore batches",
		})
	}
	group.Examples = append(group.Examples, clihelp.Example{
		Description: "Start a new batch from your request file:",
		Command:     "batches submit requests.jsonl",
	})
	setCompleteHelpContent(batches, group)
	for name, examples := range map[string][]clihelp.Example{
		"browse": {{Description: "Choose a batch without copying its ID:", Command: "batches browse"}},
		"list": {
			{Description: "Find batch IDs and their status:", Command: "batches list --max-items 10"},
		},
		"submit": {
			{Description: "Upload your file and create one batch:", Command: "batches submit requests.jsonl"},
			{Description: "Upload, start, wait, and save results:", Command: "batches submit requests.jsonl --wait --output-dir ./results"},
		},
		"create":   {{Description: "Reuse an uploaded input file; replace file_example with its ID:", Command: "batches create --input-file-id file_example --endpoint /v1/responses --completion-window 24h"}},
		"retrieve": {{Description: "Check a batch; replace batch_example with its ID:", Command: "batches retrieve batch_example"}},
		"wait":     {{Description: "Wait and save results in a new directory:", Command: "batches wait batch_example --output-dir results --wait-timeout 5m"}},
		"results": {
			{Description: "Read answers beside their original inputs:", Command: "batches results batch_example --match-input"},
		},
		"inputs": {
			{Description: "Find an uploaded input file:", Command: "batches inputs --search requests.jsonl"},
		},
		"download": {{Description: "Save a batch's output without overwriting a file:", Command: "batches download batch_example --output results.jsonl"}},
		"cancel":   {{Description: "Request cancellation of an active batch:", Command: "batches cancel batch_example"}},
	} {
		if command := batches.Command(name); command != nil {
			setCompleteHelpContent(command, clihelp.Content{Description: command.Description, Examples: examples})
		}
	}
}
